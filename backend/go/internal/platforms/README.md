# 第二部分 Go 平台模块

契约基线为 `6233234`，并消费 Java 已提交的 `9e7886f` / `10c8cb9` 完整组与目录补充。共享执行内核已接入第一部分提交 `9a05540`。这里只生成平台事实、校验冻结清单并调用共享发布器，不拥有业务资格、游标数据库、监控、重试调度或上传队列。

## 已实现与入口

| 范围 | 入口与行为 | 状态 |
| --- | --- | --- |
| B10 收藏扫描 | `telegram.Scan` 调用 `SavedClient.SavedPage`，对遇到的稳定 groupId 调用 `SavedGroup`，完整组包含跨页和增量边界前的全部成员；未完整组保持 false | 模块与替身测试通过；账号适配尚待接入 |
| B10 分类 | `telegram.Classify` 使用平台媒体属性及 MIME；语音、圆形视频明确分类，预览缩略图不成为附件；不按扩展名或正文猜测 | 已实现 |
| B10 下载 | `telegram.Download` 只处理 Java 冻结附件；PHOTO、DOCUMENT、UNKNOWN 同样保留，UNKNOWN 按 DOCUMENT 输出；每附件恰好一个媒体输出和同级 `.metadata.json` 侧车 | 已接入共享文件发布与恢复，真实平台客户端仍缺失 |
| B11 错误 | `platforms.Fault` 返回固定中文原因、阶段、重试等待、已完成/待补齐 outputId；不回显原始平台错误或私有地址 | 已实现；问题持久化由 Java 负责 |
| B12 目录 | `quark.List` 保留根实际 ID、父链、纯节点名字、版本、权限；选择器列出直属目录，托管分享遍历完整后代；分页结束后复查父目录 revision | 模块与替身测试通过；实际平台客户端尚待接入 |
| B12 转存目标 | `quark.ValidateTarget` 精确检查节点 ID、目录类型、可写性和冻结 revision，不回退到根或同名目录 | 已实现校验；未实现真实转存副作用 |
| B12 下载 | `quark.Download` 要求节点版本和 outputs 一一对应，接受 AUTO/NONE；由传入 Publisher 绑定本批本机根，不读取或更改云盘目标 | 执行计划及本地替身验证 |

扫描会返回所有类型，包括纯文字、纯照片、纯文档和照片加文档，供 Java 持久扫描范围并统一判断资格。Go 没有另一份“只下载音视频”条件。`complete` 只描述页遍历是否结束，`scannedThroughMessageId` 为实际扫描边界；完整组补收可超出该边界。Java 必须在事务内合并消息/附件/待执行动作，再把确认游标限制在任何未完整组之前。结束页仍有未完整组时不得立即循环重扫或标记初扫完成。

## 集成边界

`SavedClient` / `quark.Client` 是平台事实接口。SavedGroup 必须证明是同一账号 Saved Messages 中的稳定组，不能用相邻消息推断完整。夸克 revision 必须反映父节点、权限和内容变化，不能只取文件名或大小；未知大小是 null，零字节是 `"0"`。

`DownloadClient` 只打开冻结对象的字节流，须核验实际对象版本并响应 context 取消。它不能扫描并加入额外文件，也不能因版本失效下载同名替代项。当前没有生产 MTProto/tdl 或夸克 HTTP 绑定、没有登录入口；不得注册这些平台为已可用并返回样例成功。

`binding.Publisher` 复用第一部分 `executor.Engine.Existing/WorkFile/ExecuteFile/Pending`，核验已发布文件后复用，仅缺失项调用惰性 OpenSource，在真实字节和持久收据核对后返回 FileEvidence。平台模块不重复实现这套内核。失败返回部分文件事实及 completedEffects/pendingEffects，重试只补缺项；未完成的单个流重新获取，不宣称 MTProto 分块续传。Telegram 音频、视频、照片使用同一锁定版 FFprobe 检查媒体流，DOCUMENT/FILE 保留任意文件语义。侧车保留原始消息并提取该附件所属消息的正文、时间、来源等字段，写实际字节数。测试中的 testPublisher 仅用于较小的合同用例；`binding` 测试另用真实共享发布器验证重开、部分失败恢复、文件损坏和临时媒体。

`binding.Worker` 实现已有 `runner.Worker` 与 `Recover`，组合第一部分 Handler；目标授权和 rootId 仍调用原 Handler 的 VALIDATE_TARGET，所有文件按 Java 的 destination/outputs 写入。共享 loop 只增加两处接入：按 Worker.SupportsPlatform 判断已装配平台，按 RunnerFailure 保留受控错误/等待/完成与未完成清单。租约、日志、确认与重放继续用原实现。未提供平台客户端时不会宣称相应扫描能力。

装配入口是 `binding.Worker{Base: baseHandler, Saved: savedClient, Telegram: attachmentClient, Tree: quarkClient, Quark: quarkDownloadClient}`，将其传给现有 `runner.Runner.Worker`。当前 `cmd/runner` 没有这些真实客户端的会话装载配置，默认命令仍只装配第一部分 Handler。新增适配器接口通过本地控制端 HTTP 替身验证，不能把这点遗漏为“生产命令已支持 Telegram/夸克”。

转存执行还缺少真实 provider 的持久意图、结果不明核对与会话绑定；`TRANSFER_QUARK_ITEMS` 未注册为可用能力。`ValidateTarget` 可供该执行器在真正转存前复核准确目录，不能直接调用转存后用退出码当成功，也不能为了演示把它转换为下载。Java 第二部分负责人负责后端组合联调。

## 验证

在 `backend/go` 使用已锁定 Go 1.27.1 与 Windows cgo 工具链：

```powershell
go test -race -count=1 ./...
go vet ./...
go build ./...
```

完整临时媒体用例需要将 `ALLDOWNLOADS_TEST_FFMPEG_DIR` 设为已安装的 FFmpeg 9.0.2 的 bin 目录。未设置时 `TestRealPhotoAudioAndMetadataUseSharedPublication` 与第一部分真实 FFmpeg 用例跳过，其余测试仍运行。此变量只对当前测试进程有效。

`internal/platforms/*_test.go` 覆盖全部消息类型、可靠媒体属性、跨页/跨增量边界完整组、重复身份、结束页未完整组、超出 JavaScript 安全整数的消息 ID、取消、混合附件部分失败与补齐、逐附件侧车、冻结范围、批次本机目标、夸克根/父子/重名/空目录、目录移动/权限/版本失效及分页期间变化。

`binding` 的 HTTP 用例验证 QUARK 操作经过共享注册/领取/结果日志，结果确认丢失后重放相同 eventId，再次尝试只补失败文件，中文错误与精确完成/待补齐清单保留。它是本地控制端替身，不是 Java 进程。真实账号、Telegram 收藏历史/分组保证、夸克平台版本语义、真实下载/转存/归档、Y7000P 部署、Java-Go 组合链路和七天运行验收不在上述通过结论内。
