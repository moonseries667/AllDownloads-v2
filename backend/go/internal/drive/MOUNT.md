# rclone 共同认证源与 G 盘挂载

当前采用用户明确指定的 Y7000P 配置 `C:\Users\WIN\AppData\Roaming\rclone\rclone.conf` 中的 `gdrive`。该路径由 rclone 工具内部使用；模型不读取或展示其内容，Go 不复制 token/client secret/refresh token 到新目录。此前新 DPAPI OAuth 模式保留为另一显式认证源，不与本模式互相回退。没有回环 token 服务，也不另建 Google 身份。

## main / 发布包的准确字段

协调者的私有 runner JSON：

```json
{
  "driveAccounts": {
    "drive-main": {
      "proxyURL": "http://127.0.0.1:40000",
      "rclone": {
        "executable": "<部署目录>\\tools\\rclone-1.74.4\\rclone.exe",
        "configFile": "C:\\Users\\WIN\\AppData\\Roaming\\rclone\\rclone.conf",
        "remote": "gdrive",
        "bindingFile": "<新v2运行目录>\\auth\\drive-binding.json"
      },
      "rootId": "root"
    }
  },
  "driveMount": {
    "enabled": true,
    "accountId": "drive-main",
    "cacheDir": "<新v2运行目录>\\cache\\gdrive"
  }
}
```

`proxyURL` 位于 `driveAccounts.drive-main`，只作用于该 Drive 账号，不是全 Go 的全局代理。`driveAccounts` 的解码与互斥规则由协调者 main 单写；上面的嵌套 rclone 节为组件接入示例，最终字段以协调者 main 为准。**driveMount 的三字段已经按协调者方案确定**，可直接使用本包 `MountSettings`：

| 字段 | 类型、默认与来源 |
| --- | --- |
| enabled | bool，省略为 false；true 时 main 创建/启动挂载服务，启动失败只记录受控 NOT_MOUNTED，不阻止其余 Java/Go 启动。 |
| accountId | string，enabled=true 时固定 `drive-main`；取已经创建且注入代理 HTTP 的同一个 Client。 |
| cacheDir | string，enabled=true 时必须提供新 v2 `runtime/cache/gdrive` 的绝对路径；没有旧缓存回退。 |

`G:`、`MyGDrive` 固定。remote、rclone exe、准确 conf、代理均从该 Client 的 `RcloneConfig` 派生，**driveMount 不再重复配置**。rclone 锁 **1.74.4**；可使用 Y7000P 现有 `D:\Program Files\rclone\rclone.exe` 或发布包中的同版工具，由 main 决定准确绝对路径。启动等待默认30秒，内部 `MountConfig.StartupTimeout` 可设置1–120秒；不增加 JSON 字段。新 cache 必须与旧 `D:\Code\RcloneCache` 独立。配置存在、进程启动、挂载事实、云端归档完成分别判断。

## Go 组件类型和装配

```go
type RcloneCommand func(context.Context, string, time.Duration, []string, []string) ([]byte, []byte, error)
type RcloneConfig struct {
    AccountID, RootID string
    Executable, ConfigFile, Remote, BindingFile, ProxyURL string
    Run RcloneCommand
}
type MountSettings struct {
    Enabled bool `json:"enabled"`
    AccountID string `json:"accountId"`
    CacheDir string `json:"cacheDir"`
}
type MountConfig struct {
    CacheDir string
    StartupTimeout time.Duration
}
type MountProcess interface {
    Done() <-chan struct{}
    Stop() error
}
type MountStarter func(context.Context, string, []string, []string) (MountProcess, error)
```

```go
c, err := drive.NewRclone(drive.RcloneConfig{
    AccountID: "drive-main", RootID: "root",
    Executable: rcloneExecutable,
    ConfigFile: `C:\Users\WIN\AppData\Roaming\rclone\rclone.conf`,
    Remote: "gdrive", BindingFile: newBindingPath,
    ProxyURL: "http://127.0.0.1:40000", Run: runControlledRcloneExact,
})
c.HTTP = proxyHTTP // main 的 netpolicy.HTTPClient；所有原生 API/身份核对沿用它
m, err := drive.NewMounter(c, drive.MountConfig{CacheDir: newCacheDir}, startManagedMount)
err = m.Start(runnerLifetimeContext)
status := m.Status()
err = m.Close()
```

`Run` 是必需的共享 media 适配回调，签名参数顺序为 ctx、exe、timeout、**exactEnvironment**、args。必须无 shell、Job 控制、有界 stdout/stderr；输出只在 Go 内存供本包解析，不能记录/展示，也不能放测试 captures。构造不读文件/联网/执行工具。没有 Run、非绝对路径、非 `gdrive` remote 或非指定 WARP 代理均明确拒绝配置。

`MountStarter` 适配协调者提供的 `media.StartManaged(ctx,exe,args,exactEnvironment)`。返回的 ManagedProcess 至少实现 Done/Stop；Stop 必须幂等、有界，确认自身 Job 内进程树停止。stdout/stderr 丢弃。此包不复制 Job Object 控制代码。main 的生命周期 context 必须长于单次租约，退出时 Close；认证成功不创建媒体任务。

`Start(ctx)` 已内含一次成功挂载后的监控协程，异常时只停止自身挂载，**不包含自动恢复/重启循环**。Root 可用单个周期 Ensure 重试未挂载状态；MOUNT_ALREADY_RUNNING 不另开实例，MOUNT_STOP_UNCONFIRMED 保留所有权并拒绝重启。`Status()` 不联网或刷新认证，只检查本地进程、盘符与配置元数据；main 将内部 STOPPED/STARTING/ERROR 映射为公共 NOT_MOUNTED。`RcloneConfig.Run` 的准确 shim 为 `media.RunControlledExactEnvironment(ctx,exe,timeout,env,args...)`，由 main 装配共享实现，不在本包复制进程代码。

## 认证与恢复语义

1. 首次使用先本地核对 rclone version，再执行 `about gdrive: --json --config <准确conf>`。这只核对 quota/能力并允许 rclone 正常刷新，不枚举或导入媒体。随后仅执行 `config show gdrive --config <准确conf>`，在内存解析个人 OAuth 的 type、token、client_id、root_folder_id 等必要字段。不会 dump 其他 remote，也不手动覆盖/复制源配置；rclone 的正常 token 更新仍写它自己的源配置。
2. access token 只短期留在内存；通过注入的 c.HTTP 查询 Drive permissionId 和 Google tokeninfo 的 issued_to，核对实际账号和 OAuth 客户端。新 BindingFile 只保存非秘密 accountId、permissionId、clientId、准确源路径/remote、remote 根标识。后续身份、客户端、remote 根变化均拒绝并保留原绑定。文件元数据变化会重新核对，不能因缓存静默接受另一个账号。
3. 只支持个人 Drive OAuth。shared drive、service account、env-auth、client-credentials、impersonation 或自定义 OAuth 端点返回明确 unsupported 原因。配置未指定 root_folder_id 时使用实际 My Drive；已指定时原生 API 使用该真实受控根，不猜另一个默认目录。main 另外指定的 RootID 不得扩大或替换 remote 的受控根。
4. `StartOAuth` 返回 `AUTH_MANAGED_EXTERNALLY`，main/Java 映射 LOCAL_SETUP，说明由用户在正式主机维护该准确 rclone remote；已有有效配置不要求再建 Desktop OAuth 客户端。失效时不启动交互配置、不换号、不回退 DPAPI/匿名认证。
5. `Archive`/`Verify`/`Reconcile` 共用原身份绑定。原生 API 不自行刷新 Google token；需要续期时仍经 rclone about，再解析该 remote。挂载使用同一个 conf/remote，由 rclone 正常共享其配置更新；没有第二份长期认证文件。原上传恢复沿用 [README](README.md) 的稳定远端 ID 与冻结清单。

## 挂载与代理

参数固定为 `mount gdrive: G: --config <准确conf> --volname MyGDrive --network-mode`，并沿用 VFS full、max-size5G、max-age12h、dir-cache24h、read-chunk64M/limit256M、buffer64M、transfers32、poll1m、INFO。只使用新 cache-dir。参数不含 token/client secret。环境剔除继承的 RCLONE_* / Google 覆盖，仅允许既有 `RCLONE_CONFIG_PASS` 在工具内部解锁准确配置；HTTP_PROXY/HTTPS_PROXY 大小写均固定为 `http://127.0.0.1:40000`，NO_PROXY 大小写清空，ALL_PROXY 清空。代理故障不转为直连，程序不改系统代理/WARP/防火墙。

已有任何 G 盘占用均拒绝。WinFsp 通过只读安装探测检查，缺失返回 WINFSP_REQUIRED，本包不安装。只有自己的子进程仍存活且新网络盘实际出现、指向 `\\server\MyGDrive`，才返回 MOUNTED。`Status()` 只返回 State/ReasonCode/Remote/MountPoint/VolumeName；main 映射动态注册事实。配置变化未复核时不报告 MOUNTED；监测到身份变化、代理失效、子进程退出或盘符消失后结束自己的挂载，不杀全机 rclone，不覆盖/移除他人的盘。停止无法确认时保留所有权和缓存锁，拒绝第二次启动。

挂载不证明远端文件完整。强制停止、掉电时可能仍有 VFS 待写回，新 cache 保留，不清空、不伪报已归档；下一次用相同 cache/source 启动仍须核对远端事实。rclone 挂载读写与 Go 的固定 Artifact 上传是不同用户操作，不能用 Explorer/VFS 状态替代归档收据。

## 验证范围

Carbon 只运行合成配置输出、HTTP 身份响应、真实临时绑定文件和进程/盘符替身；没有调用 Y7000P 的真实 conf，没有真实 Google/OAuth、WinFsp 挂载或 Explorer 验收。回归覆盖：精确单 remote 命令、WARP 环境、绑定不含凭据、身份/客户端/root改变、unsupported认证、占用G/缺WinFsp/代理故障、无盘不成功、退出/取消/停止失败和配置变更停止。共享 Job API 的真实子进程测试由其负责人提供。

用户手传完整 ZIP 后，Y7000P 的实际挂载、WARP、退出与缓存恢复由协调者验收。配置、绑定、缓存、媒体均不进入源码包；只带无凭据模板和锁定工具。

依据：[1.74.4 config 命令](https://github.com/rclone/rclone/blob/v1.74.4/cmd/config/config.go)、[只显示单 remote](https://rclone.org/commands/rclone_config_show/)、[about --json](https://rclone.org/commands/rclone_about/)、[Windows mount](https://rclone.org/commands/rclone_mount/)、[Google tokeninfo 官方 discovery](https://www.googleapis.com/discovery/v1/apis/oauth2/v2/rest)、[WinFsp 官方加载探测](https://github.com/winfsp/cgofuse/blob/master/fuse/host_cgo.go)。
