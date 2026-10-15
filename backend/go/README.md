# Go 执行核心（M1 本地文件替身）

此模块只负责本地合成 fixture 的文件 I/O、发布事实、私有执行收据与恢复，不连接 Java 业务数据库，不包含业务调度或平台认证。公开 HTTP 与 Java-Go transport 尚待 #2 契约冻结；当前没有可供生产启动的常驻 runner。

## 构建与测试

锁定 Go 1.27.1（Windows amd64），没有外部 Go 模块依赖，因此没有 go.sum。使用对应 Go 和支持 race 的 MinGW-w64 编译器，在本目录执行：

```powershell
go test -race -count=1 ./...
go vet ./...
go build ./...
```

Windows race 需启用 cgo，并使支持的 gcc 或 clang 可被发现；本地可在当前终端设置 `CGO_ENABLED=1` 与准确 `CC` 路径，不修改系统运行时。CI 已提供 Windows cgo 工具链预检。构建工具缓存与测试临时文件不进入 Git。

## 内部接口与证据

`internal/executor` 的 `Open(stateDir, outputRoot)` 显式指定隔离目录，`Execute(ctx, Request)` 写入不超过 1 MiB 的合成 UTF-8 内容，返回实际路径、十进制字节数与 SHA-256。`Pending()` 返回等待持久确认的成功收据；`Acknowledge(operationID, ackID)` 只记录已收到确认，不删除历史。它们是模块内部结构，不是共同 wire schema。

执行前先持久化不可变意图；临时文件放 `.work`，关闭并核对后才发布。重复请求必须匹配原意图，文件已就位时核对真实内容后补报，不能凭收据或退出码推定成果存在。不同内容、损坏文件、已有目标及路径冲突明确失败，不覆盖。运行目录保持 OS 排他锁，进程退出释放；引擎内串行持有写入职责。

测试覆盖取消/期限、重复请求、发布后收据未保存的故障恢复、收据重开/确认、文件损坏、危险路径、并发重复及多实例拒绝。均为 Carbon 临时数据和本地文件测试；不包含真实平台请求、上传、媒体续传或生产数据。

## 接续工作

当前语义基线 `dc53d92`，参见 [执行协议](../../contracts/RUNNER.md)。[#2](https://github.com/moonseries667/AllDownloads-v2/issues/2) 需要冻结领取、续租、报告、reconcile、身份认证与 fixture 的公共测试入口后，才能添加正式 transport、租约丢失的在途停止与运行循环；不会让这里的私有类型代替冻结协议。

Java 对 SQLite 调度及确认事务的测试由 [Java 模块](../java/README.md) 承担；两个模块各自通过不代表跨进程闭环或 [#5](https://github.com/moonseries667/AllDownloads-v2/issues/5) 三端联调通过。Y7000P、平台账号、媒体下载/归档、电源断电与七天运行验收均未执行。
