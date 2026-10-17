# 显式本地 HTTP/SOCKS5 代理

供 Telegram、tdl 子进程和 Google Drive 接入同一已配置的本地代理。仅决定指定客户端的连接路径，不修改系统、Clash、WARP、父进程环境或 `http.DefaultTransport`，不安装依赖，也不实现平台调度或 G 盘挂载。

```go
client, err := netpolicy.HTTPClient(proxyURL, 30*time.Second)
dial, err := netpolicy.DialContext(proxyURL)
overrides, err := netpolicy.Environment(proxyURL)
```

三个构造函数仅检查配置，不发起网络连接。接受明确的 `http://回环IP:端口` 或 `socks5://回环IP:端口`，例如 `http://127.0.0.1:40000`、`socks5://127.0.0.1:40000`、`socks5://[::1]:40000`。端口必须是 1—65535；允许末尾 `/`，会规范化。仅支持匿名代理；拒绝 userinfo、query、fragment、其他路径、HTTPS 或其他 scheme、DNS 主机名（包括 localhost）及非回环地址。错误不会包含配置值、认证信息或上游响应正文。

## HTTP 客户端

`HTTPClient(proxyURL string, timeout time.Duration) (*http.Client, error)` 为每次调用克隆标准默认 transport，设置传入的 Client.Timeout。显式 HTTP 代理使用固定 `ProxyURL`，SOCKS5 使用同一配置的隧道拨号函数；两者均不读取环境代理或服从 NO_PROXY，回环目标也经过该代理，失败绝不改用直连。HTTPS 在 HTTP CONNECT 或 SOCKS5 隧道内继续使用标准 TLS 并验证源站证书。HTTP 和 CONNECT 的 407 均返回 `ErrProxyAuthentication`；SOCKS5 要求认证时同样拒绝；其他握手拒绝返回 `ErrProxyHandshake`；连接错误和超时分别映射为受控错误。

空字符串保留默认 transport 的行为，包括其原有 ProxyFromEnvironment；它表示没有新增显式代理策略，不表示强制直连。每个 client 都有独立连接池，可用 `client.CloseIdleConnections()` 关闭自己的空闲连接。

调用方通过 `errors.Is` 判断 `ErrInvalidProxy`、`ErrProxyUnavailable`、`ErrProxyAuthentication`、`ErrProxyHandshake`、`ErrProxyTimeout`。取消和上下文超时保留 `context.Canceled` / `context.DeadlineExceeded`。本组件不返回原始网络错误细节；标准 `http.Client.Do` 仍会将错误包装成含业务请求 URL 的 `url.Error`，平台层应按错误码记录结果，不能直接记录含签名参数的请求 URL。

## TCP 隧道

`DialContext(proxyURL string) (func(context.Context, string, string) (net.Conn, error), error)` 可作为 gotd/td 等客户端的拨号函数。显式配置只接受 tcp/tcp4/tcp6，目标是 host:port；拒绝非 TCP 和无效目标，不把调用参数当 URL 或命令解释。代理跳本身使用其回环 IP 的地址族，支持 IPv6 目标通过 IPv4 本地代理建立隧道；目标主机名由代理解析。

拨号和 HTTP CONNECT/SOCKS5 握手最多 15 秒，并服从更早的上下文 deadline；取消会关闭正在建立的连接。HTTP CONNECT 响应头最多 64 KiB，仅 200 视为隧道成功，407、其他状态、断开和畸形响应都明确失败。SOCKS5 只接受匿名方法和成功的 CONNECT 响应，支持 IPv4、IPv6 及代理端解析目标域名；认证要求、拒绝、断开和畸形响应均失败。两种方式都不连接目标作为 fallback。成功后保留解析响应时提前缓冲的数据，解除 HTTP 响应头大小限制，并清除握手读写 deadline；已返回连接的生命周期由调用者管理，后续取消拨号 context 不会关闭它。空字符串返回普通 `net.Dialer.DialContext`。

## 子进程环境

`Environment(proxyURL string) (map[string]string, error)` 返回待合入 `exec.Cmd.Env` 的覆盖值：HTTP_PROXY、HTTPS_PROXY、ALL_PROXY 指向规范化代理地址，NO_PROXY 为空；同时覆盖对应小写同义名，避免工具优先采用继承的 `no_proxy` 绕过代理。该函数不调用 os.Setenv，也不读取或输出父进程环境值。

调用方先保留自身原有子进程环境，再按变量名替换/追加返回值；Windows 按不区分大小写的名字处理。不要用这些返回值修改父进程环境。空字符串返回 nil，调用方保留原环境即可。HTTP_PROXY、HTTPS_PROXY、ALL_PROXY 的地址均保留配置的 HTTP 或 SOCKS5 scheme，不做协议转换。子进程是否消费环境代理由其自身实现决定；tdl 必须显式传入 `--proxy`，原生 MTProto 客户端应接入同一策略的隧道 dialer，不能仅凭设置环境变量声称已经代理。

## 验证与接入边界

在 `backend/go` 执行：

```powershell
go test -race -count=1 ./internal/netpolicy
go vet ./internal/netpolicy
go build ./internal/netpolicy
```

测试只用回环 HTTP/TLS/TCP origin 和 HTTP/SOCKS5 代理：验证 HTTP 不受 NO_PROXY 绕过、两种隧道中的 HTTPS 源站证书验证、TCP 双向转发、提前缓冲数据及超过 64 KiB 的后续隧道流、握手取消/超时、成功后 deadline 清理、407/认证要求/拒绝/断开无直连降级、输入拒绝、transport 隔离和父环境不变。

本模块不连接 Y7000P 的真实 40000 端口、不访问平台账号或外部网络。正式入口配置、Telegram/tdl/Drive 接入、WARP 端口实际 HTTP/CONNECT/SOCKS5 能力及资源管理器中的 G 盘由协调者分别集成和验收；不能把本地替身通过写成这些结果已经完成。
