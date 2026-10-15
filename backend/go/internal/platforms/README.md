# 第二部分 Go 平台模块

契约基线为 `6233234`，并消费 Java 已提交的 `9e7886f` 完整组与目录补充。这里只生成平台事实、校验冻结清单并调用共享发布器，不拥有业务资格、游标数据库、监控、重试调度或上传队列。

## 已实现与入口

| 范围 | 入口与行为 | 状态 |
| --- | --- | --- |
| B10 收藏扫描 | `telegram.Scan` 调用 `SavedClient.SavedPage`，对遇到的稳定 groupId 调用 `SavedGroup`，完整组包含跨页和增量边界前的全部成员；未完整组保持 false | 模块与替身测试通过；账号适配尚待接入 |
| B10 分类 | `telegram.Classify` 使用平台媒体属性及 MIME；语音、圆形视频明确分类，预览缩略图不成为附件；不按扩展名或正文猜测 | 已实现 |
| B10 下载 | `telegram.Download` 只处理 Java 冻结附件；PHOTO、DOCUMENT、UNKNOWN 同样保留，UNKNOWN 按 DOCUMENT 输出；每附件恰好一个媒体输出和同级 `.metadata.json` 侧车 | 执行计划及本地替身验证；发布器由共享内核提供 |
| B11 错误 | `platforms.Fault` 返回固定中文原因、阶段、重试等待、已完成/待补齐 outputId；不回显原始平台错误或私有地址 | 已实现；问题持久化由 Java 负责 |
| B12 目录 | `quark.List` 保留根实际 ID、父链、纯节点名字、版本、权限；选择器列出直属目录，托管分享遍历完整后代；分页结束后复查父目录 revision | 模块与替身测试通过；实际平台客户端尚待接入 |
| B12 转存目标 | `quark.ValidateTarget` 精确检查节点 ID、目录类型、可写性和冻结 revision，不回退到根或同名目录 | 已实现校验；未实现真实转存副作用 |
| B12 下载 | `quark.Download` 要求节点版本和 outputs 一一对应，接受 AUTO/NONE；由传入 Publisher 绑定本批本机根，不读取或更改云盘目标 | 执行计划及本地替身验证 |

扫描会返回所有类型，包括纯文字、纯照片、纯文档和照片加文档，供 Java 持久扫描范围并统一判断资格。Go 没有另一份“只下载音视频”条件。`complete` 只描述页遍历是否结束，`scannedThroughMessageId` 为实际扫描边界；完整组补收可超出该边界。Java 必须在事务内合并消息/附件/待执行动作，再把确认游标限制在任何未完整组之前。结束页仍有未完整组时不得立即循环重扫或标记初扫完成。

## 集成边界

`SavedClient` / `quark.Client` 是平台事实接口。SavedGroup 必须证明是同一账号 Saved Messages 中的稳定组，不能用相邻消息推断完整。夸克 revision 必须反映父节点、权限和内容变化，不能只取文件名或大小；未知大小是 null，零字节是 `"0"`。

`DownloadClient` 只打开冻结对象的字节流，须核验实际对象版本并响应 context 取消。它不能扫描并加入额外文件，也不能因版本失效下载同名替代项。当前没有生产 MTProto/tdl 或夸克 HTTP 绑定、没有登录入口；不得注册这些平台为已可用并返回样例成功。

`platforms.Publisher.Ensure` 是第一部分共享收据/文件发布内核的接入点：先固定意图和受控本机目标，核验已发布文件后复用，仅缺失项调用惰性 OpenSource，并在真实字节和持久收据核对后返回 FileEvidence。平台模块不重复实现这套内核。失败返回部分文件事实及 completedEffects/pendingEffects，重试只补缺项。测试中的 testPublisher 仅用于替身合同验证，不作为生产恢复证明。

转存执行还依赖共享内核持久意图、结果不明核对和真实 provider。不得直接调用转存后用退出码当成功，也不能为了演示把它转换为下载。第一部分通用 runner 的已完成提交接入后再注册操作处理器；Java 第二部分负责人负责后端组合联调。

## 验证

在 `backend/go` 使用已锁定 Go 1.27.1 与 Windows cgo 工具链：

```powershell
go test -race -count=1 ./...
go vet ./...
go build ./...
```

`internal/platforms/*_test.go` 覆盖全部消息类型、可靠媒体属性、跨页/跨增量边界完整组、重复身份、结束页未完整组、超出 JavaScript 安全整数的消息 ID、取消、混合附件部分失败与补齐、逐附件侧车、冻结范围、批次本机目标、夸克根/父子/重名/空目录、目录移动/权限/版本失效及分页期间变化。

这些为 Carbon 临时文件和替身验证。真实账号、Telegram 收藏历史/分组保证、夸克平台版本语义、真实下载/转存/归档、Y7000P 部署、Java-Go 组合链路和七天运行验收不在上述通过结论内。
