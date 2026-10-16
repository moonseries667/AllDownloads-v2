# Bilibili 单账号会话

本组件只管理固定逻辑账号 `bilibili-main`。Carbon 只运行隔离测试；新会话文件、真实扫码与读下载在 Y7000P。不会读取浏览器、旧 AllDownload 会话或系统凭据，不建立账号池、轮换、自动切换或匿名降级。

## 装配

由正式入口配置一个不入库的绝对 `cookieFile`。首次新会话可以省略 `expectedUserId`，用户显式扫码成功后才绑定真实 UID，并将 UID 与 Cookie 一起保存到新私有会话文件。再次读取、下载或登录必须是该 UID；清空配置 UID 不会清除文件内的绑定。可选 `expectedUserId` 仅提供额外约束，必须是十进制正整数。同一账号更换新会话可继续使用。构造不读取文件、不联网；文件不存在也不妨碍启动和 BeginQR。省略文件路径配置时，正式 main 应以自身私有 `stateDir/bilibili/session.json` 为默认目标，不能用浏览器或旧应用路径。

```go
cookiePath := cfg.Bilibili.CookieFile
if cookiePath == "" {
    cookiePath = filepath.Join(cfg.StateDir, "bilibili", "session.json")
}
session, err := bilibili.New(bilibili.Config{
    CookieFile: cookiePath,
    ExpectedUserID: cfg.Bilibili.ExpectedUserID, // optional on first QR login
})
if err != nil { return err }
provider := &media.Bilibili{Session: session}
downloads := media.Downloader{Client: session.DownloadClient()}
// runner.Handler{Provider: provider, Downloads: downloads, ...}
```

`cmd/runner/main.go`、配置模板和公共协议由协调者装配，本目录不修改它们。没有 Session 的 `media.Bilibili` 返回 `AUTH_REQUIRED`，传入普通 HTTP client 也不会恢复匿名读取。现有 Handler 在 accountRef 非空时会拒绝操作，账号引用的适配由协调者处理；本组件不另建公共接口。

二维码流程创建的私有文件是带 schemaVersion、userId 和 cookie 的 JSON，由 Go 单独保存，不能交给网页或 Java。也支持用户为 v2 提供的单行 Cookie 请求头，但这种输入必须同时配置 expectedUserId，不能从原始 Cookie 隐式建立未知身份。Cookie 必须包含 `SESSDATA` 和与绑定 UID 一致的 `DedeUserID`，不接受 Netscape 格式、重复字段或多行注入。Windows 下文件所在目录应是 Y7000P 当前运行用户的私有运行目录；Go 的 0600 不等同于 Windows ACL 配置，本组件不修改系统 ACL。

## 身份与下载

`Bind(ctx) (*http.Client, string, error)` 读取唯一受控文件，通过 `/x/web-interface/nav` 同时核对 `code=0`、`isLogin=true` 和精确 MID，再返回绑定该次 Cookie 的 client 与 UID。文件内身份、已核对身份与可选配置 UID 必须一致。收藏夹/合集的所有分页和视频资料共用一次绑定，即使文件中途更新也不混合会话。HTTP client 不继承 cookie jar；平台 Set-Cookie 不会自动改变会话。

私有媒体快照只新增 `X-Alldownloads-Bilibili-Identity: <UID>`，轨道版本为 `bilibili:<UID>:<trackID>`。Cookie 不写入 Source.Headers、Java 结果、资料、错误或日志。旧匿名快照不能用新下载 client 发起请求，须重新刷新资料。

`DownloadClient()` 在每次网络请求与重定向前重新核对相同 UID，然后使用该身份取得的签名媒体地址。Cookie 只发给精确 API origin；CDN、字幕和弹幕域名不带 Cookie。所有实际请求均移除私有身份头、外部传入的 Cookie、Authorization 和 Proxy-Authorization。下载只接受 HTTPS 的 `api.bilibili.com`、`comment.bilibili.com`、`bilivideo.com/cn/net`、`hdslb.com` 及后三类的真实子域。API 重定向直接作为认证失败，不跟随到登录页。没有外域 fallback。

失效、缺失或身份不符均为 `AUTH_REQUIRED`；限流为 `RATE_LIMITED`；未知响应和网络失败保持失败。播放、字幕和弹幕接口的认证失败会使整个资料读取失败，不作为可用资料警告保存。权限、付费、地区和其他原有错误语义保持。下载器会把 transport error 统一成网络错误，因此本组件用本地 401 响应保留认证失败，其他检查失败返回受控 HTTP 错误，不伪造成功正文。

## 检查与二维码入口

```go
account, err := session.Check(ctx)
flow, err := session.BeginQR(ctx)
flow, err = session.PollQR(ctx, flow.ID)
flow, err = session.CancelQR(flow.ID)
```

`Account` 仅含 AccountID、PlatformUserID、State、ReasonCode。内部 READY 对应公共 ACTIVE；AUTH_REQUIRED 对应公共 AUTH_REQUIRED，UNAVAILABLE 对应 ERROR；`Check` 同时返回受控 error，协调适配层应保存 account 事实。没有文件时需要登录，不代表配置存在就已经 ACTIVE。

`QRLogin` 仅含 ID、State、URL、ExpiresAt、ReasonCode。ID 是本地随机流程标识，不是平台扫码密钥。内部状态与公共模型映射如下：

| 内部状态 | 公共 state / nextAction |
| --- | --- |
| WAITING_SCAN | PENDING / SCAN_QR |
| WAITING_CONFIRM | PENDING / WAIT |
| READY | SUCCEEDED / NONE |
| CANCELLED | CANCELLED / NONE |
| EXPIRED | EXPIRED / NONE |
| FAILED | NEEDS_ACTION / NONE |

由协调者映射 `authSessionId -> flow.ID`，关联 Java 的 taskId/revision；不改变共享协议。进程重启后内存流程丢失，旧 ID 返回 `AUTH_FLOW_NOT_FOUND`，应映射 NEEDS_ACTION 并由用户重新发起。不会伪恢复或自动重新生成二维码。

Begin 只请求官方扫码地址；URL 只交给当前本机登录页面生成二维码，不记录到常规日志，不发送到第三方二维码服务。用户亲自在 Bilibili App 扫码并确认。Poll 每两秒最多一次，流程最多三分钟，只在显式登录流程中请求，不启动后台轮询器。平台风控或协议变化明确失败，不切换 TV/API 签名方案或绕过验证。

确认成功后，仅从官方 poll 的 Set-Cookie 提取登录 Cookie，先通过 nav 核对真实 UID。首次显式登录可绑定该 UID；已保存身份、当前进程已核对身份或可选配置 UID 存在时必须一致。之后经临时文件同步并替换私有会话文件。callback URL 与 refresh_token 均忽略，不回显、不持久。签入其他 UID、过期 Cookie、验证失败或取消均保留原文件。Cancel 可停止在途请求，不删除现有会话、不撤销用户在平台端已完成的扫码确认。

## 验证

在 `backend/go` 执行：

```powershell
go test -race -count=1 ./internal/media ./internal/platforms/bilibili
go vet ./internal/media ./internal/platforms/bilibili
go build ./...
```

本地替身覆盖缺失/重复/错误 Cookie、精确 UID、过期且仍含 WBI key 的响应、整次来源刷新绑定、私有快照无凭据、同 UID 更新、下载前认证、跨域重定向无凭据、各资料阶段认证失败、二维码等待/确认/过期/取消/错误身份与原文件保护。`IsolatedTestMode` 明确开启后，API/Passport 覆盖只能是回环 IP HTTP；生产默认地址不能被任意替换。

尚需 Y7000P 真实新账号扫码、收藏夹读取、用户明确选择后的媒体下载及跨 Java/Go/页面验收。本地替身不证明平台当前风控允许登录，也不证明正式入口已完成装配。

平台请求参考 [Bilibili 官方登录入口](https://passport.bilibili.com/login) 与作者维护的 [Kodi Bilibili 登录实现](https://github.com/chen310/plugin.video.bili/blob/master/addon.py)；HTTP client 的 jar、重定向和 RoundTripper 行为依据 [Go net/http](https://pkg.go.dev/net/http)。未调用真实账号或获取业务媒体。
