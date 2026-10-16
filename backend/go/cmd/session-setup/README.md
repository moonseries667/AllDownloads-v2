# 本地新会话设置

`session-setup.exe` 只供用户在 Y7000P 的交互式 Windows 终端中运行。它读取明确指定的 v2 runner 配置，负责 Telegram 新二维码授权或夸克新 Cookie 输入；不启动业务调度，不扫描收藏，不转存、不下载、不发送消息。模型、前端、Java 和业务日志均不接收凭据。

## 发布与使用

在 `backend/go` 构建，发布者把该文件与 runner、tdl 0.20.4 一起放入发布包：

```powershell
go build -trimpath -o bin/session-setup.exe ./cmd/session-setup
```

以下命令由用户在 Y7000P 本地桌面终端运行。正式部署根为 `D:\Code\Other\AllDownloads-v2`，配置为 `runtime/config/runner.local.json`；从 `app/current.json` 的 `version` 读取当前发布目录。先停止本版本进程，避免它持有旧配置或正在使用会话。命令行只包含配置路径和平台名，不接受 Cookie、密码、验证码或 token 参数。

```powershell
$root = 'D:\Code\Other\AllDownloads-v2'
$version = (Get-Content -LiteralPath (Join-Path $root 'app/current.json') -Raw | ConvertFrom-Json).version
$setup = Join-Path $root "app/releases/$version/session-setup.exe"
& (Join-Path $root 'scripts/release/Stop-AllDownloads.ps1') -Root $root
```

确认停止成功后，按需设置对应平台；以下命令沿用上面的变量：

```powershell
& $setup --config (Join-Path $root 'runtime/config/runner.local.json') --platform telegram
& $setup --config (Join-Path $root 'runtime/config/runner.local.json') --platform quark
```

Telegram：正式配置的 `telegram.proxyURL` 为 Y7000P 已有 WARP HTTP 代理 `http://127.0.0.1:40000`。CLI 从同一配置读取它；只运行指定 tdl 的 `--proxy http://127.0.0.1:40000 --ns alldownloads-v2 --storage type=bolt,path=<新目录> login --type qr`。在终端扫码，必要时在该终端输入两步验证密码。stdout、stderr、stdin 直接连用户终端，不捕获登录输出、不解析本地化成功提示。授权后通过独立 Self 请求读取当前用户，原子更新 `telegram.accounts.telegram-main.peerId`，作为 Saved Messages 绑定身份。已绑定时仅检查原身份，不能通过重新设置换号。

同一个显式代理用于所有 tdl 命令，包括 Self、收藏导出与附件下载。地址仅接受无 userinfo 的本地 HTTP 代理；配置无效或代理连接失败均返回错误，不改用直连。tdl 本地扩展协议中的代理必须与子进程的 `TDL_PROXY` 一致，Self 的 gotd 连接通过同一 HTTP CONNECT 代理建立。`HTTP_PROXY`、`HTTPS_PROXY`、`ALL_PROXY` 和空 `NO_PROXY` 只传给 Telegram 子进程，父进程及 B 站、夸克环境不变；不会启动或修改 WARP。新会话绑定保留 `telegram.proxyURL` 与其他未知配置字段。

夸克：用户在 Y7000P 的新浏览器中亲自登录官方站点，再从该新会话复制 Cookie，粘贴到隐藏输入提示。程序只访问 `https://pan.quark.cn/account/info` 核对 UID，先原子保存绑定，再原子保存 `cookieFile`。程序不读取浏览器资料，不读取未绑定的既有 Cookie。绑定后更新 Cookie 必须仍为同一 UID。首次保存若中断，保留已验证 UID，重新运行可继续保存该账号 Cookie。

确认所需平台的 setup 成功后，使用正式启动脚本读取新配置：

```powershell
& (Join-Path $root 'scripts/release/Start-AllDownloads.ps1') -Root $root
```

再由用户在网页账号页发起检查。CLI 也提供只核对绑定身份的命令，沿用上面的变量：

```powershell
& $setup --config (Join-Path $root 'runtime/config/runner.local.json') --platform telegram --check
& $setup --config (Join-Path $root 'runtime/config/runner.local.json') --platform quark --check
```

缺失账号条目时，以 `stateDir` 的同级 `auth` 创建新路径；例如 `runtime/runner` 对应 `runtime/auth/telegram` 和 `runtime/auth/quark-cookie.local`。已有明确配置必须仍在这一新私有目录内。Telegram executable 必须明确配置为绝对路径。已有模板中的 `REPLACE_WITH_NEW_SESSION_USER_ID` 和 `REPLACE_WITH_NEW_SESSION_QUARK_UID` 视为首次未绑定。未知配置字段保留，不能覆盖不同身份。配置、会话和运行目录应由部署流程预先授予运行用户私有 ACL；Go 的 `0600` 不替代 Windows ACL。

设置进程持有 `<runner配置>.session-setup.lock`。异常退出后，只有确认该设置进程已停止，才手动删除准确的锁文件并重试。不会自动删除会话或旧数据。若既有未绑定 Telegram 私有存储无法验证，不覆盖它：先排除网络或版本问题，或由部署者为 v2 配置全新私有目录。

## 身份接口与 tdl 限制

- `(*telegram.TDL).CheckSession(ctx, accountID) (selfID string, err error)`：要求已绑定 PeerID，只运行版本检查与 Self 扩展，实时身份不同则返回 `ACCOUNT_IDENTITY_CHANGED`。
- `(*telegram.TDL).CurrentIdentity(ctx, accountID)`：仅首次本地设置允许空 PeerID，已有绑定仍必须匹配。
- `(*quark.NativeClient).CheckSession(ctx) (uid string, err error)`：要求已绑定 ExpectedUserID，只读 account/info。
- `(*quark.NativeClient).IdentifyForSetup(ctx)`：允许首次空 UID，存在绑定时仍必须匹配。

tdl 0.20.4 没有公开的纯身份 CLI 命令。`chat export` 即使指定零条或极小范围仍会建立历史迭代器，`chat ls --filter` 也会先枚举聊天，均不用于身份检查。实现使用官方本地扩展协议：将当前发布的 `session-setup.exe` 复制到新 tool-home 内的 `tdl-alldownloads-self` 扩展目录。tdl 内部将该新会话传给本地辅助进程；辅助进程使用与 tdl 一致的 `gotd/td v0.140.0`，关闭日志和更新，仅调用 `Self`（users.getUsers/inputUserSelf），返回 `{"selfId":"..."}`。没有独立扩展下载或额外发布资产。

所有 tdl 子进程的 `USERPROFILE`、`HOME` 指向 `stateDir/telegram-work/tool-home`；临时扩展协议文件也限定在其 `tmp`，退出由 tdl 删除。登录限制五分钟，身份检查限制四十五秒，辅助网络请求限制三十秒。程序不读取默认 tdl 或 Telegram Desktop 存储，命令显式选择 QR 类型。

官方源代码依据：

- [tdl QR 登录与 Self](https://github.com/iyear/tdl/blob/v0.20.4/app/login/qr.go)
- [tdl 本地扩展协议](https://github.com/iyear/tdl/blob/v0.20.4/cmd/extension.go)、[内部传递方式](https://github.com/iyear/tdl/blob/v0.20.4/pkg/extensions/manager.go)
- [tdl 扩展数据结构](https://github.com/iyear/tdl/blob/v0.20.4/extension/extension.go)
- [tdl 显式代理接线](https://github.com/iyear/tdl/blob/v0.20.4/core/tclient/tclient.go)、[HTTP CONNECT 支持](https://github.com/iyear/tdl/blob/v0.20.4/core/util/netutil/netutil.go)
- [tdl 导出会访问历史](https://github.com/iyear/tdl/blob/v0.20.4/app/chat/export.go)
- [gotd Self 实现](https://github.com/gotd/td/blob/v0.140.0/telegram/self.go)

## 验证边界

本地测试使用独立临时配置、合成 Cookie、loopback HTTP 替身及受控 tdl 命令替身，覆盖首次绑定、错号拒绝、配置并发变化、路径边界、锁、字段保留和无历史访问的命令契约。代理回归覆盖所有命令传递、父环境不变、userinfo 拒绝、Self 的 CONNECT 隧道和失败不直连。Windows 控制台隐藏输入、真实扫码、平台身份、远端 WARP、ACL 与持续运行必须在 Y7000P 由用户交互验收；构建和替身通过不代表这些已验证。
