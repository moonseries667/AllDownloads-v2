# Telegram / 夸克平台模块

契约基线为 `6233234`，并消费 Java 已提交的 `9e7886f` / `10c8cb9` 完整组与目录补充。共享执行内核已接入第一部分提交 `9a05540`。这里只生成平台事实、校验冻结清单并调用共享发布器，不拥有业务资格、游标数据库、监控、重试调度或上传队列。

## 已实现与入口

| 范围 | 入口与行为 | 状态 |
| --- | --- | --- |
| B10 收藏扫描 | `telegram.Scan` + 正式 `telegram.TDL`，完整 Saved Messages 导出及私有组索引；保留跨页和增量边界前成员 | 正式装配及本地协议测试通过，真实账号待验收 |
| B10 分类 | `telegram.Classify` 使用平台媒体属性及 MIME；语音、圆形视频明确分类，预览缩略图不成为附件；不按扩展名或正文猜测 | 已实现 |
| B10 下载 | `telegram.Download` + `TDL.OpenAttachment`，单条冻结消息输入，逐附件媒体与同级 `.metadata.json`，不使用 --group 扩大范围 | 正式装配及文件/取消/恢复测试通过 |
| B11 错误 | `platforms.Fault` 返回固定中文原因、阶段、重试等待、已完成/待补齐 outputId；不回显原始平台错误或私有地址 | 已实现；问题持久化由 Java 负责 |
| B12 目录 | `quark.NativeClient` + `List`，真实 UID/节点/父链/分页；托管视图只纳入已证明的转存身份 | 正式装配及 HTTP 协议测试通过 |
| B12 转存目标 | `quark.Transfers` 保存意图/源快照/固定目标/平台 task 与 remote ID，完成后核对强校验值；不确定先只读核对 | 原生 HTTP 实现与超时/重启/增量协议测试通过 |
| B12 下载 | `quark.Download` 要求节点版本和 outputs 一一对应，接受 AUTO/NONE；由传入 Publisher 绑定本批本机根，不读取或更改云盘目标 | 执行计划及本地替身验证 |

扫描会返回所有类型，包括纯文字、纯照片、纯文档和照片加文档，供 Java 持久扫描范围并统一判断资格。Go 没有另一份“只下载音视频”条件。`complete` 只描述页遍历是否结束，`scannedThroughMessageId` 为实际扫描边界；完整组补收可超出该边界。Java 必须在事务内合并消息/附件/待执行动作，再把确认游标限制在任何未完整组之前。结束页仍有未完整组时不得立即循环重扫或标记初扫完成。

## 集成边界

`SavedClient` / `quark.Client` 是平台事实接口。SavedGroup 必须证明是同一账号 Saved Messages 中的稳定组，不能用相邻消息推断完整。夸克 revision 必须反映父节点、权限和内容变化，不能只取文件名或大小；未知大小是 null，零字节是 `"0"`。

`DownloadClient` 只打开冻结对象字节流，核验实际版本并响应取消，不加入额外文件或回退同名对象。正式绑定分别为 `telegram.TDL` 和 `quark.Transfers.OpenNode`。新会话的用户授权及配置见 [Go 主说明](../../README.md)，未配置/过期返回受控认证状态，不返回样例成功。

`binding.Publisher` 复用第一部分 `executor.Engine.Existing/WorkFile/ExecuteFile/Pending`，核验已发布文件后复用，仅缺失项调用惰性 OpenSource，在真实字节和持久收据核对后返回 FileEvidence。平台模块不重复实现这套内核。失败返回部分文件事实及 completedEffects/pendingEffects，重试只补缺项；未完成的单个流重新获取，不宣称 MTProto 分块续传。Telegram 音频、视频、照片使用同一锁定版 FFprobe 检查媒体流，DOCUMENT/FILE 保留任意文件语义。侧车保留原始消息并提取该附件所属消息的正文、时间、来源等字段，写实际字节数。测试中的 testPublisher 仅用于较小的合同用例；`binding` 测试另用真实共享发布器验证重开、部分失败恢复、文件损坏和临时媒体。

`binding.Worker` 实现已有 `runner.Worker` 与 `Recover`，组合第一部分 Handler；目标授权和 rootId 仍调用原 Handler 的 VALIDATE_TARGET，所有文件按 Java 的 destination/outputs 写入。共享 loop 只增加两处接入：按 Worker.SupportsPlatform 判断已装配平台，按 RunnerFailure 保留受控错误/等待/完成与未完成清单。租约、日志、确认与重放继续用原实现。未提供平台客户端时不会宣称相应扫描能力。

正式 `cmd/runner` 已构造 `binding.Worker{Base,Saved,Telegram,Tree,Quark,Transfers}`，再包 `StorageWorker`。具体操作匹配由 SupportsOperation 判定；缺账号的已支持操作返回 AUTH_REQUIRED，不因通用 DOWNLOAD_MEDIA 能力误发到另一平台。

TRANSFER_QUARK_ITEMS 和只读 RECONCILE_QUARK_TRANSFER 已注册。未知 save 结果不重发；证明所有已发效果且剩余未发后，Java 才允许恢复原 operation。转存完成或追更仍不创建本机订单。事实语义与样例见 [正式客户端契约](../../../../contracts/PLATFORM-CLIENTS.md)。

## 验证

在 `backend/go` 使用已锁定 Go 1.27.1 与 Windows cgo 工具链：

```powershell
go test -race -count=1 ./...
go vet ./...
go build ./...
```

完整临时媒体用例需要将 `ALLDOWNLOADS_TEST_FFMPEG_DIR` 设为已安装的 FFmpeg 9.0.2 的 bin 目录。未设置时 `TestRealPhotoAudioAndMetadataUseSharedPublication`、`TestTelegramKeepsAdditionalStreamsInOriginalAttachment` 与第一部分真实 FFmpeg 用例跳过，其余测试仍运行。此变量只对当前测试进程有效。Carbon 本轮设置了该变量，上述用例实际执行并通过。

`internal/platforms/*_test.go` 覆盖全部消息类型、可靠媒体属性、跨页/跨增量边界完整组、重复身份、结束页未完整组、超出 JavaScript 安全整数的消息 ID、取消、混合附件部分失败与补齐、逐附件侧车、冻结范围、批次本机目标、夸克根/父子/重名/空目录、目录移动/权限/版本失效及分页期间变化。

`binding`、`telegram/tdl_test.go`、`quark/native_transfer_test.go` 覆盖正式适配器、CLI 文件协议与原生 HTTP、强校验、重启未知结果、增量及冻结下载。`tests/integration/platform_clients_process.py` 另外启动真实 Java 和正式 Go 命令，使用本地平台 HTTP 与 tdl 协议子进程验证首扫/监控/实际流取消、只读转存核对、失败补齐、确认丢失和重启。后者仍是隔离平台协议验收；真实账号、平台媒体、Y7000P 与七天运行待独立验收。
