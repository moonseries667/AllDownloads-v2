# Drive 持久认证与恢复

本包负责 Google Drive I/O 和 v2 私有认证。Carbon 仅运行临时文件、本地 HTTP 和 Windows DPAPI 测试；真实 Google 授权、目录选择、上传和恢复验收在 Y7000P 进行。没有读取旧会话或浏览器凭据，没有使用付费服务或上传业务媒体。

## 接入接口

现有 `Client{AccountID,RootID,Token}` 保持可用，短期 token 仍由调用方提供。正式持续运行使用：

```go
c, err := drive.NewGoogle(drive.GoogleConfig{
    AccountID: "drive-main",
    RootID: "root", // 或事先核对的 My Drive 子目录 ID
    TokenFile: privateTokenPath,
    ClientFile: newlyProvidedDesktopClientPath,
})
// 构造不读凭据、不联系 Google、不启动登录。
err = c.Check(ctx) // 主动只读核对身份、允许根目录与列举/写入能力
flow, err := c.StartOAuth(ctx)
flow, err = c.PollOAuth(flow.ID)
flow, err = c.CancelOAuth(flow.ID)
```

`TokenFile` / `ClientFile` 是绝对路径，仅指向用户为 v2 新建的运行文件；不能指向旧应用或浏览器配置。`ClientFile` 仅授权时读取，刷新不依赖它仍然存在。令牌文件保存必要客户端信息、access/refresh token、到期时间、本地账号引用和 Google `permissionId`，不进入 Java、业务结果、日志、Git 或命令参数。公开结构没有 token 字段。

`OAuthSession` 包含 `ID, State, AuthorizationURL, ExpiresAt, ReasonCode`。接口只返回官方授权 URL；授权码、state 校验过程、PKCE verifier 和 token 不返回调用方。调用方不要记录完整回调 URL或客户端 JSON。每个客户端同时只允许一个未结束流程；Java 应复用同一 `authSessionId` 对应的组件 ID，而不是重复 BEGIN。会话五分钟到期；结束后清空授权 URL。

| 组件结果 | 协调者的公共映射 |
| --- | --- |
| PENDING | PENDING / OPEN_URL，返回 AuthorizationURL |
| SUCCEEDED | SUCCEEDED / NONE；已核对 Google 身份并保存新认证 |
| CANCELLED、EXPIRED | 对应同名状态 / NONE |
| FAILED | NEEDS_ACTION / NONE，使用受控 ReasonCode |
| AUTH_CLIENT_REQUIRED | NEEDS_ACTION / LOCAL_SETUP，请用户提供新 Desktop 客户端 JSON |
| AUTH_SESSION_NOT_FOUND | NEEDS_ACTION / LOCAL_SETUP，明确流程已丢失，由用户重新授权 |

授权流程仅保存在当前进程内。操作请求结束不取消用户流程；显式取消、五分钟超时或进程退出会结束监听。POLL 不等待网络交换；CANCEL 能中断正在进行的 token 请求。已原子保存成功的认证保留成功事实，取消不删除账号认证。协调者持久记录公共流程与组件 ID；重启后不得用 BEGIN 假装恢复旧流程。

runner 配置和 AccountWorker 由协调者单写。本包建议私有 `driveAccounts.drive-main.tokenFile/clientFile/rootId`；`tokenEnv` 与 `tokenFile` 互斥。此建议不是本分支自行增加的公共 HTTP 字段，当前基线入口尚未装配这些接口。固定公共账号为 `drive-main`。

## Y7000P 新授权

1. 在确认的 v2 运行目录中准备新的 private 子目录。由用户在 Google Cloud 为此应用启用 Drive API，并新建 **Desktop app** OAuth 客户端，下载其 JSON 到该私有目录。程序不创建云项目、不代替用户同意授权，也不读取既有客户端凭据。若控制台要求新增费用或其他权限，应由用户另行决定。
2. 协调者把新 JSON 路径和新的 `TokenFile` 路径配置给 `NewGoogle`。用户通过账号页启动 Google Drive 授权，由 runner 返回官方 URL。用户须在 **Y7000P 的系统浏览器**打开它并亲自选择账号、同意。回调只监听 `127.0.0.1` 的临时端口，使用随机 state 和 PKCE S256；在 Carbon 打开此 URL不能回调到 Y7000P。
3. 授权完成后，Go 只读获取 Google `about.user.permissionId`，核对已绑定身份，再原子保存认证。新授权必须取得 refresh token 和所需 scope。之后主动调用 `Check`；刷新认证与检查都不会扫描收藏或上传媒体。
4. 在目录选择器调用 `List(ctx,"","",pageSize)`，逐级选择真实 My Drive 目录，再以该项 `ID/Revision` 调用 `Validate`。由 Java 保存返回 Target 并冻结到新订单。不要手填显示路径代替 folderId，不选 shared drive；在途订单继续使用原冻结目标。

目录选择需要列举用户已经存在的文件夹，因此当前授权请求完整 `https://www.googleapis.com/auth/drive` scope；没有把仅能访问本应用文件的 `drive.file` 当成通用目录树权限。用户可在官方授权页查看权限并拒绝。Google 对外部应用的 **Testing** 发布状态通常只签发七天 refresh token，不能据此声称可无限持续运行；用户需按 Google 政策完成合适的发布配置。撤销、失效或管理策略限制返回 AUTH_REQUIRED，由用户重新授权同一 Google 身份，不自动换号。

Windows 令牌文件用当前用户的 DPAPI 加密（不使用机器级解密），先写同目录临时文件、Sync/Close，再 MoveFileEx 原子替换。刷新和重新授权持有同一路径的跨进程排他锁；旧文件在刷新失败、身份变化、scope 不足或保存失败时保留。授权与正式 runner 必须使用 Y7000P 的同一 Windows 用户（当前 ssh yp 为 Admin，profile 为 `C:\Users\WIN`）；不能从 Carbon 复制令牌文件，也不能跨用户复用。非 Windows 文件要求 0600；此平台仅为本地开发兼容，不构成正式部署验收。

## 刷新与上传核对

请求前读取私有文件，access token 提前一分钟刷新；并发读共享一次刷新。Google 不返回新的 refresh token 时保留原值，返回新值时同事务保存。每个新 access token、进程重启后的首次使用都只读核对 `permissionId`。首次身份检查遇到提前撤销的 token，只允许一次刷新和一次只读重试；普通 Drive 401 则使该 token 到期，返回明确错误，由下次操作先刷新。POST/PUT 不在 HTTP 层自动重放。

上传收据额外绑定 `accountId + Google permissionId + OAuth clientId` 的摘要，且一次上传的每个请求都核对同一绑定。`Archive` 和 `Verify` 不能由另一个身份继续，即使它能读取共享目录；OAuth 重新授权也不能替换已绑定 Google 身份或客户端。旧 tokenEnv 收据未记录真实 Google 身份，不自动迁移到新认证；原收据保留，可由原客户端核对。业务结果仍只有既有归档字段。

遇到 RESULT_UNKNOWN：

1. 保留本地媒体、原操作 ID、冻结清单、目标和 Go 执行状态目录；先恢复同一账号认证。不要创建新上传任务来猜测旧结果。
2. 使用 `Verify(ctx,state,originalUploadOperationID,localRoot,frozenRequest)`。传原上传操作 ID，不能拿新核对操作的 ID 查旧收据。Verify 仅查询已保存的远端 ID、父目录、名称、大小、应用标记和 SHA-256/MD5，不预留 ID、不创建目录、不重发媒体。必要本地文件仍须存在并符合冻结清单。
3. 全部事实吻合才返回 `Verified=true`。缺失、名称/父级/大小/校验不符、目录变化、身份或权限问题保留待处理状态；没有足够证据时不伪完成。
4. 需要继续上传时由 Java 按用户动作和恢复策略安排原意图。再次 `Archive` 先核对旧 ID；只有缺失项才继续使用同一预生成 ID。Drive 会拒绝已经存在的 ID，避免产生同名重复对象。不能删除收据、换 operationId 或猜另一个目标来绕过核对。

## 本次验证与复现

在 `backend/go` 中，使用项目 Go 1.27.1 和已有 Windows cgo 编译器：

```powershell
$env:CGO_ENABLED = '1'
$env:CC = 'D:\Code\Env\Tools\llvm-mingw\bin\x86_64-w64-mingw32-clang.exe'
$env:PATH = 'D:\Code\Env\Tools\llvm-mingw\bin;' + $env:PATH
go test -race -count=1 ./internal/drive ./internal/runner
go vet ./internal/drive
go build -o bin/runner.exe ./cmd/runner
```

`auth_test.go` 使用临时凭据、真实临时文件、Windows DPAPI 和本地 HTTP：并发刷新、重启复用、省略/轮换 refresh token、撤销与刷新失败、恶意错误正文脱敏、scope/身份变化、重定向拒绝、文件锁、OAuth state/PKCE、取消/轮询、上传响应丢失及上传401后的只读核对。原目录/清单/校验与相对目录恢复测试继续保留。

尚未验证：协调者 AccountWorker/Java/前端的组合认证链路、Y7000P 用户完成 Google OAuth、真实 Drive API/目录/上传及长时间运行。没有把本地 HTTP 通过算作真实账号验收。

依据：[Google 桌面 OAuth 与 PKCE](https://developers.google.com/identity/protocols/oauth2/native-app)、[Refresh token 生命周期](https://developers.google.com/identity/protocols/oauth2#expiration)、[Drive about.get](https://developers.google.com/workspace/drive/api/reference/rest/v3/about/get)、[Drive User.permissionId](https://developers.google.com/workspace/drive/api/reference/rest/v3/User)、[预生成 ID 与上传重试](https://developers.google.com/workspace/drive/api/guides/manage-uploads)、[Windows DPAPI](https://learn.microsoft.com/en-us/windows/win32/api/dpapi/nf-dpapi-cryptprotectdata)。
