# WARP 代理与资源管理器挂载

用户在 2026-10-17 指定以 Y7000P 的 `D:\Code\gdrive.bat` 为依据：tdl 与 Google Drive 使用 Cloudflare WARP 的 `http://127.0.0.1:40000`。该设置仅应用于这些客户端/子进程，配置后的代理故障明确报错，不回退直连，不修改系统代理、Clash、WARP 服务或防火墙。Bilibili 保持单账号读取与下载。

用户另明确提供 `C:\Users\WIN\AppData\Roaming\rclone\rclone.conf`。Google Drive 原生 API 与 G 盘使用同一 `gdrive` 授权源，由 rclone/Go 在程序内部使用，不将凭据展示给模型、复制进发布包或新业务库。既有 rclone 的正常认证刷新仍由其工具完成。此指定仅是 Google 授权来源的例外，不迁移旧媒体、任务或数据库，不恢复旧后台。

## 私有运行配置

`telegram.proxyURL` 作用于 QR 登录、Self 身份辅助程序、收藏导出和下载。`driveAccounts.drive-main.proxyURL` 作用于原生 Google HTTP、身份核对、rclone 认证与挂载；两者均为上述准确地址。完整无凭据结构见 [发布模板](../scripts/release/templates/runner.example.json)。

Drive 的 `rclone` 节包含 `executable,configFile,remote,bindingFile`；remote 固定 `gdrive`，可执行工具锁定 rclone 1.74.4。`bindingFile` 是新 v2 身份元数据，不含 access/refresh token 或 client secret。首次只读核对实际身份、客户端及 remote 根，后续变化不得替换旧上传意图。原 DPAPI OAuth 是另一个显式认证源，不能与 rclone 混用或自动互相降级。

`driveMount` 为 `{enabled,accountId,cacheDir}`，省略 enabled 为 false；发布模板启用 `drive-main`，缓存为新 `<Root>/runtime/cache/gdrive`。G: 与 MyGDrive、network-mode、VFS full、5G/12h、目录缓存 24h、64M/256M 读取分片、64M 缓冲、32 transfers、1m poll 沿用参考脚本；不使用旧缓存目录。

## 事实与用户动作

Go 使用自身 Job Object 管理挂载子进程。G: 被占用时不覆盖，缺 WinFsp 时明确报告，不自动安装。只有自有进程存活且实际网络盘映射吻合才提供 `mount.state=MOUNTED`；进程启动、配置存在或缓存写入不构成该事实。父进程退出只结束自身挂载，未写回缓存保留；云端归档仍按冻结清单与真实远端校验判定。

公共 Runtime 的 mount 结构保持 `{state,reasonCode,observedAt}`，沿用已有状态枚举。内部 STARTING/ERROR/STOPPED 映射 NOT_MOUNTED，缺驱动为 UNSUPPORTED。执行程序定期只采样本地事实并重新注册，不能在该回调扫描 Telegram 或执行平台任务。挂载/代理尚未就绪时 Java、Go、网页保留，状态明确为待处理，恢复后不需重启其他平台。

`AUTH_MANAGED_EXTERNALLY` 对应 LOCAL_SETUP，提示用户在 Y7000P 维护指定 rclone remote，不要求为该认证模式新建 Desktop OAuth client。既有有效授权不触发登录。Y7000P 的真实代理、WinFsp/MPR、G 盘 Explorer 可见性与停止后卸载须在用户传输发布包后验收；Carbon 的替身不替代它们。
