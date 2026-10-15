# Java 与 Go 执行协议

第三部分独立子项执行、部分停止收据、上传固定目标与原生/Drive 目录操作见 [第三部分冻结契约](FEEDBACK-PART3.md)。

第一部分实际 HTTP 字段、媒体执行 payload 和目录验证见 [第一部分冻结契约](FEEDBACK-PART1.md) 与 [机器 schema](runner.schema.json)。

## 1 所有权与通信

Java 是业务状态、业务队列和调度决定的唯一所有者。Go 是平台会话、真实 I/O 和执行收据的唯一所有者。Go 不连接业务数据库，不在执行完成后自行创建另一个平台下载；Java 不自行执行媒体下载、浏览器自动化或外部命令。

正式 Java 与 Go 均运行在 Y7000P，业务数据库、账号会话、执行收据和下载目录也归该机所有。Carbon 构建与测试使用隔离实例和临时数据，不参与正式任务执行。首版采用 Y7000P 同机回环 HTTP JSON 拉取协议，内部前缀 `/internal/v1`，内部认证与浏览器会话分离。只有一个 Go 实例持有运行目录排他锁；错误恢复不能通过同时启动第二个实例解决。协议版本为 `1`，能力握手不兼容时禁止接单并说明缺少的操作。

这套协议提供至少一次指令投递与幂等结果处理。它不承诺外部平台操作恰好执行一次；外部写入的不确定结果必须通过收据与平台核对处理。

## 2 操作模型

```json
{
  "protocolVersion": 1,
  "operationId": "op-example",
  "taskId": "task-example",
  "attempt": 1,
  "leaseId": "lease-example",
  "fencingToken": "1",
  "leaseExpiresAt": "2026-10-15T10:00:00Z",
  "operationType": "DOWNLOAD_MEDIA",
  "platform": "QUARK",
  "accountRef": "account-example",
  "resourceKey": "node-example@version-example",
  "storageRevision": "1",
  "destination": {"rootId":"download-root","relativePath":"Quark/12345678/课程/第一课.mp4"},
  "payload": {"nodeId":"node-example","version":"version-example"}
}
```

operationId 标识一个逻辑步骤，重试时稳定；attempt 标识执行尝试；每次租约 fencingToken 单调增加，阻止旧结果覆盖新结果。accountRef 是 Go 可解析的不透明本机会话引用，不是 Cookie 或 token。payload 按 operationType 使用独立类型校验，不传任意命令行、脚本或待执行代码。

| operationType | 输入语义 | 结果语义 |
| --- | --- | --- |
| SYNC_BIL_SOURCE | 平台来源标识、快照批次 | 内容及成员分页、扫描完成标记 |
| FETCH_BIL_METADATA | 视频或部分标识、所需信息 | 规范化资料、可用质量及缺项原因 |
| SCAN_TEL_SAVED | 账号、起始消息 ID | 全部扫描消息范围、目标资源、最高已完整收录 ID |
| RESOLVE_TEL_LINKS | 消息链接清单 | 资源、媒体组、文字与有效性 |
| CHECK_QUARK_SHARE | 完整分享标识、目标云盘目录、已知快照 | 新增/变化/失效/未知清单 |
| TRANSFER_QUARK_ITEMS | 已确认的待转存对象和固定目录 | 实际转存映射与待核对项 |
| LIST_QUARK_TREE | 实际云盘节点范围 | 节点快照、版本依据及遍历完成标记 |
| CREATE_QUARK_FOLDER | 父节点、名称、稳定操作标识 | 真实文件夹标识或不确定结果 |
| DOWNLOAD_MEDIA | 平台资源版本、目录计划、质量与部分范围 | 临时/成品身份、字节数、校验依据 |
| FINALIZE_ARTIFACT | 已下载文件清单、必要元数据 | 成品清单、实际路径、可用性与缺项 |
| UPLOAD_ARTIFACT | 不可变成品清单、云端目标引用 | 云端实际对象清单与未完成项 |
| VERIFY_ARCHIVE | 成品清单和云端对象清单 | 每项核对结果及整体是否完整 |
| PROBE_ACCOUNT / AUTH_STEP | 账号引用或官方登录步骤 | 账号能力、状态或下一登录步骤 |

这些是内部业务操作类型，不要求各自起一个进程或公开一套 HTTP 服务。平台适配器通过同一执行接口提供不同实现。

## 3 生命周期接口

| 路径 | 含义 |
| --- | --- |
| POST `/runners/register` | Go 上报 runnerId、协议版本、能力和工具版本 |
| POST `/leases/acquire` | 按能力和剩余配额领取一个操作；无任务返回空结果 |
| POST `/leases/{id}/heartbeat` | 续约并报告进度；响应包含 desiredAction |
| POST `/operations/{id}/facts` | 分页资料或执行事实，带唯一 eventId 与顺序号 |
| POST `/operations/{id}/result` | 上报最终结果及收据摘要，带 attempt 与 fencingToken |
| POST `/operations/{id}/reconcile` | 重启后报告已知执行情况，获取恢复决定 |

所有写请求均允许携带相同 eventId 重发；Java 事务内去重后返回持久化确认。同一 eventId 若内容不同返回冲突。Go 只有拿到持久确认才能标记事实已交付。

设计默认心跳每十五秒一次，租约九十秒。Go 接近租约到期且续约失败时停止启动新副作用步骤，并有界停止本地执行；已发出的远程请求可能完成，记为待核对。Java 在租约过期时标为失联待恢复，不能直接把同一外部写入分配给另一个执行者。

Java 返回 desiredAction=CONTINUE/PAUSE/CANCEL。Go 必须传播取消到网络请求与子进程，完成必要的文件关闭和收据持久化后上报停止结果。远程转存无法立即撤回时如实报告待核对，不能伪称操作从未发生。

## 4 收据和幂等

Go 在私有执行日志记录 operationId、资源版本、目标映射、当前执行阶段、子进程身份、临时文件、成品清单和待交付事实。该日志可以采用独立 SQLite，单写入者；其用途是恢复真实 I/O，不保存第二份业务调度策略。

收到重复 operationId 时先查收据：已完成则重发结果；正在运行则返回当前执行；未确认副作用则先核对。每个资源/最终路径只有一个执行锁。仅仅拒收旧 fencingToken 不能阻止旧进程写文件，因此还须运行实例排他锁、文件写入锁和实际停止确认。

转存前持久化目标与预期对象；请求成功后持久化真实云盘映射；应答丢失时列举目标核对，不能重新转存一整份。上传同样需要固定对象身份或可核对的目标清单，避免应答丢失后创建多个同名云端文件。

Java 保存终态和后续操作必须原子提交。数据库事务完成而响应丢失时，Go 重发相同事实应返回已有确认，不再创建上传操作。数据库失败时不能返回持久成功。

## 5 重启和失联

Java 重启：从业务数据库恢复暂停开关、待执行操作、租约和未核对结果；先与 Go 对账在途操作，再继续调度。

Go 重启：获取运行锁，检查收据、临时文件和受控子进程是否仍存活；未确认旧执行已经结束前，不复用同一输出路径。重新注册后向 Java 报告已知结果，由 Java 决定继续、取消或核对。无法证明安全重入时进入 NEEDS_ACTION，不猜测成功。

电脑断电：Java 恢复业务事务，Go 恢复收据与实际文件；文件已发布但结果未确认时核对后补报，部分文件只续传适用部分。服务启动不自动取消用户已保存的暂停状态。

Java 无响应期间 Go 不无限领取或建立本地业务队列，已有操作按租约要求停止或待核对。Go 无响应期间 Java 可继续提供已有资料，新的执行保持排队并显示执行程序不可用。

## 6 错误和限流

结果状态为 SUCCEEDED/PAUSED/CANCELLED/RETRYABLE_ERROR/NEEDS_ACTION/RESULT_UNKNOWN。错误含 code、stage、retryAfter、completedEffects、pendingEffects 和可安全恢复的范围；不能返回敏感请求内容。

Java 统一决定下一次尝试。Go 内部只对明确无副作用的短操作做有限重试；对超时转存、上传提交、文件发布，必须走核对。平台限流和等待作为结构化结果传回，不能吞掉后显示 0 字节成功。

子进程必须由 Go 直接管理，不经过拼接 shell；固定 executable 与允许的参数类型，单独捕获退出码和结构化结果。Windows 上取消要处理进程树，避免父进程退出但子进程继续占用文件。日志输出有界并脱敏。

## 7 必须通过的协议测试

重复领取、重复事实、乱序进度、旧租约结果、完成响应丢失、Java 提交失败、Go 发布文件后崩溃、转存请求结果不明、上传成功但确认丢失、取消与完成并发。每个场景必须验证记录、任务和文件都一致，不能只检查 HTTP 状态码。
