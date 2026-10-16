# 正式平台客户端与只读核对（v1 增量）

本增量沿用前三部分的公共模型、Java 唯一业务队列及 Go 执行收据。机器生命周期见 [runner-lifecycle.openapi.json](runner-lifecycle.openapi.json)，共享样例及 schema 对照见 [runner-lifecycle.examples.json](runner-lifecycle.examples.json)。内部空领取固定 HTTP 200、`data.operation=null`，错误外壳与公共 API 一致；`/internal/v1` 只接受回环内部 Bearer 身份。

## 会话与正式装配

Go 正式 `cmd/runner` 装配 `StorageWorker → binding.Worker → Telegram TDL / Quark NativeClient`。Telegram 锁定 tdl 0.20.4，通过指定的新私有 namespace/storage 和固定 Saved Messages peer ID 执行；夸克通过指定的新私有 cookie 文件及预期平台 uid 绑定。普通启动只检查工具版本，不访问 Telegram；首次同步、增量和已启用监控才访问平台。凭据、私有 locator、分享令牌及提取码不进入公共账号/错误模型、进程参数或日志。

缺少配置或认证时任务明确返回 AUTH_REQUIRED / AUTH_OR_TOOL_REQUIRED；账号身份变化拒绝冻结操作。平台等待返回 RATE_LIMITED 及 retryAfterSeconds；请求、传输、工具和取消均有界。fixtureCatalog 及平台测试端点必须显式配置 isolatedTestMode=true，测试端点只接受回环 HTTP；没有生产 fallback。

Telegram 导出保留全部类型，按稳定平台 groupId 收集完整成员后由 Java 决定资格。确认游标之后正常只扫描增量；Go 私有组索引丢失时可只读修复历史组事实。纯文本、照片、文档不创建下载；含音视频单元的全部附件及逐附件侧车冻结执行。取消后自动监控不恢复旧取消实例；显式补齐创建新实例。

## 夸克追更及核对

`PATCH /quark/shares/{id} {watchEnabled:boolean}` 使用 If-Match，返回新 Share 与 ETag。`POST /quark/shares/{id}/refresh {}` 使用 Idempotency-Key，返回 202 `{taskId}`，合并同分享的活动转存。首次转存成功后默认每小时检查一次；认证、目标和未知结果等待处理，不高速重复检查。关闭监控不取消已经受理的操作。

目标、账号、完整分享标识及不可变源快照在转存前固定。每个实际 save 请求先持久化源对象、目标父节点、请求前节点基线及发送意图；平台 task ID 和实际 remote ID 随后落收据。超时、取消或进程中断不能证明未转存；没有确定结果时为 RESULT_UNKNOWN，禁止普通 RETRY。核对使用同账号、同目标、平台任务或新节点的完整内容校验，不仅比较名称和大小。新增只转存新增版本，原分享移除不删除旧副本，同名变化保留真实平台生成的新身份。

`RECONCILE_QUARK_TRANSFER` 是新增的只读 Runner 操作，平台仍为 QUARK、任务 kind 仍为 TRANSFER_SYNC。payload 是原 TRANSFER_QUARK_ITEMS 输入加 `originalOperationId`。它不发送 save、不创建本机下载。成功 result 保留 `shareId,targetFolderId,targetFolderRevision,nodes,complete`，可增加 `safeToResume:boolean`：

- complete=true：全部冻结转存效果已核验，Java 在结果事务中完成原任务并刷新受管理目录。
- complete=false 且 safeToResume=true：所有已发送请求均已核验，剩余步骤证明从未发送；只有此时 Java 才允许恢复原 operationId 和冻结清单。已保存 PAUSE/CANCEL 意图仍生效。
- 无上述证明：RESULT_UNKNOWN，保持待核对，不重发未知请求。

公共任务 RECONCILE 动作会创建独立核对任务；该核对结果 ack.taskId 是核对任务 ID，原转存任务在同一事务中按事实推进。重复 eventId 返回原 ack；变更同事件内容返回 EVENT_CONFLICT。下载的已确认本机失败可显式 RETRY，RESULT_UNKNOWN 不获得此权限。

信息刷新、转存、追更及核对从不创建 download order。受管理分享目录只包含已证明的转存身份，不把目标目录中其他文件伪造为该分享。网盘选择下载继续走原冻结 preview/order，批次 NONE、目录、单文件取消和上传资格不变。

## 其他协议补齐及消费者影响

facts 的 sequence 为非负整数，同 operation 内严格递增；同 event 的原内容可重放，低序号新事件返回 FACTS_OUT_OF_ORDER。facts 只保存事实及确认，最终 result 仍是发布业务状态和后续任务的唯一事务边界。成功下载返回严格 FileEvidence；上传返回 ArchivedFile，两个文件模型分别校验。

Drive 上传为了保留已冻结成果的相对层级，可以在选定目标下自动建立必要中间目录：先预留稳定 ID、持久映射再创建，丢应答后按同 ID 核对。目录选择、设置校验及只读 Verify 不创建目录；重名不能唯一确定时明确失败，不退回根目录。此能力不新增前端手动目录管理接口。

前端继续使用前三部分类型；新增夸克 PATCH/refresh 使用上述 revision/幂等规则，任务按钮仅消费 allowedActions。没有变更平台枚举、下载项目层级、取消对象或预览选择。真实账号、Y7000P 部署、业务媒体与七天运行单独验收；本地协议替身不构成平台验收。

隔离 fixture 的创建入口仍为测试宿主 `core.TaskStore.create`，WRITE_FIXTURE/VERIFY_FIXTURE 不注册到产品 Runner，不混入平台枚举，也不增加产品公共创建路由。正式跨进程验收使用真实产品 API、Java SQLite、正式 Go 主入口及本地平台协议替身。
