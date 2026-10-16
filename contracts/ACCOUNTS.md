# 新账号与登录流程

本增量保持公共 `/api/v1` 和 runner protocolVersion 1。Bilibili 固定一个逻辑账号 `bilibili-main`，来源读取、信息刷新及下载共用同一私有会话，不设账号池、轮换或匿名降级。其他固定逻辑账号为 `telegram-main`、`quark-main`、`drive-main`；平台名分别为 TELEGRAM、QUARK、GOOGLE_DRIVE。

## 公共接口

- `GET /accounts` 返回 `{items:Account[],nextCursor:null}`，只读已保存事实；启动和普通查询不主动访问平台。首次未核对状态为 UNKNOWN。
- Account 为 `{id,platform,label,state,lastCheckedAt,reasonCode,revision}`。state 为 ACTIVE/AUTH_REQUIRED/UNKNOWN/ERROR；只有实际身份检查成功可写 ACTIVE，配置存在不等于登录成功。无新会话为 AUTH_REQUIRED，reasonCode 为 ACCOUNT_NOT_CONFIGURED。
- `POST /accounts/{id}/check` 需要 Idempotency-Key，返回 202 `{accountId,taskId}`。只核对该账号身份和必要读权限，不扫描收藏、不创建来源、下载、转存或上传。
- `POST /auth-sessions` 需要 Idempotency-Key，输入仅 `{platform}`，返回 202 AuthSession。每个平台只有一个未结束流程，重复操作复用流程，不替换已绑定身份。
- `GET /auth-sessions/{id}` 只读。AuthSession 为 `{authSessionId,accountId,platform,state,nextAction,url,message,expiresAt,revision,taskId,reasonCode}`。state 为 PENDING/SUCCEEDED/CANCELLED/EXPIRED/NEEDS_ACTION；nextAction 为 WAIT/SCAN_QR/OPEN_URL/LOCAL_SETUP/NONE。url 仅为当前平台的官方授权地址或二维码内容，不含 Cookie、access/refresh token。客户端在本机生成二维码，不能把内容发到第三方二维码服务。
- `POST /auth-sessions/{id}/steps` 需要 Idempotency-Key，输入仅 `{action:"POLL"}`，返回 202 AuthSession。只在用户打开的未过期流程中有限轮询；GET 不排队。首次 BEGIN、随后每次 POLL 使用不同内部 operation，但同一次请求幂等。
- `DELETE /auth-sessions/{id}` 需要 If-Match，返回 202 AuthSession，立即记录取消意图并排 CANCEL；迟到结果不得将其复活。取消登录不删除已保存账号会话，不取消业务任务。

Bilibili 的 SCAN_QR 同时支持旧 `https://passport.bilibili.com/h5-app/passport/login/scan` 和 2026-10-17 在 Y7000P 实测的 `https://account.bilibili.com/h5/account-h5/auth/scan-web`。新路径只公开 `navhide=1`、`callback=close`、空 `from` 与单个非空 `qrcode_key`；必须使用 HTTPS、准确主机与路径，不接受 userinfo、fragment、额外端口或重复参数，不跟随扫码地址。原生适配器核对返回 key 一致后按对应路径重建二维码内容，其他上游字段不能进入公共会话。旧路径继续兼容。

账号错误保留受控的 `PLATFORM_RESPONSE_UNKNOWN`、`NETWORK_ERROR`、`RESOURCE_UNAVAILABLE`、`SESSION_WRITE_FAILED`、`LOGIN_IN_PROGRESS` 分类；未知错误仍归一，不输出响应正文、扫码 key、Cookie 或原始异常。`NETWORK_ERROR` 与已有临时网络错误同为可重试事实，不代表授权成功。

Google OAuth 必须由用户亲自在 Y7000P 通过官方授权页完成；没有新 Desktop OAuth client 配置时返回 LOCAL_SETUP 与明确原因。Telegram 首次登录与夸克新会话导入可在 Y7000P 的受控本地入口完成，页面明确展示 LOCAL_SETUP，再由用户主动检查；不得声称已有浏览器自动登录能力。页面、Java 数据库和日志不接收或保存密码、验证码、Cookie、OAuth code 或 token。

## 内部执行

新增 `CHECK_ACCOUNT` 和 `AUTH_STEP`。沿用平台枚举：Google Drive 的执行 envelope.platform 为 BILIBILI，payload.platform 为 GOOGLE_DRIVE；这是已有存储执行的路由约定，不代表 B 站账号。

CHECK_ACCOUNT payload 为 `{schemaVersion:1,accountId,platform}`，accountRef 必须等于 accountId，destination=null；成功 result 为 `{account:Account}`，身份不可用作为明确 Account 状态返回，不制造平台成功。

AUTH_STEP payload 为 `{schemaVersion:1,authSessionId,accountId,platform,action}`，action 为 BEGIN/POLL/CANCEL；accountRef 必须等于 accountId，destination=null。成功 result 为 `{session:AuthSession}`；taskId/revision 由 Java 管理，Go 可以省略，由 Java 从受信任操作关联补齐。Go 持久身份、二维码键和 OAuth state 于自己的私有运行目录，结果和公共字段不能携带凭据。每次请求以 authSessionId 关联同一流程，不能用另一个 operationId 重建或覆盖账号。

账号认证仅改变会话状态；媒体下载、收藏扫描和夸克转存仍须相应用户动作或明确启用的既有监控。认证成功不自动重放未知远程写入。
