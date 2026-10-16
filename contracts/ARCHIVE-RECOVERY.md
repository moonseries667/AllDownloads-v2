# 上传核对与恢复

`UPLOAD_ARTIFACT` 的 NEEDS_ACTION 允许显式 RECONCILE。Java 只从原持久 operation 复制冻结 accountRef、destination、storageRevision 和上传 payload，新建或复用 `VERIFY_ARCHIVE`，不创建新上传身份。该类型已有于 v1 枚举；payload 精确为 `{schemaVersion:1,originalOperationId,upload}`，upload 为原 UploadArtifactPayload。

Go 以原 originalOperationId 调用 Drive 只读核对，成功 result 为 `{originalOperationId,complete,safeToResume,archive,reasonCode}`。archive 为完整 ArchiveResult 或 null；complete=true 时 archive 必须完整 verified，safeToResume=false，reasonCode=null。complete=false 时 archive=null、reasonCode 为受控错误码。核对不预留新 ID、不创建目录、不上传字节。

只有原上传的私有持久计划仍存在、身份/冻结目标/本机清单全部匹配，已观察对象没有冲突时，Go 才能给 safeToResume=true。已发写入必须有预先持久化的固定 ID；后续尚无 ID 的项可依据“先持久预留 ID，后发送写入”的不变式证明未发送。核对仍检查全部已有对象，不能让较早的缺项掩盖后续冲突。没有持久计划、身份变化、目录变化、校验冲突、权限或网络失败均不能推断安全续传。

Java 完整核验后把事实交给原上传处理器，并确认原任务；safeToResume=true 只重排原 operationId，保留 PAUSE/CANCEL 意图。其余保持原 NEEDS_ACTION。只读核对任务完成或失败后，用户可以再次核对；不能永久复用一个不可领取的终态核对任务。

明确的只读 operation（来源/媒体元数据、账号检查、目录读取与校验）在 NEEDS_ACTION 可由用户显式 RETRY 原意图。UPLOAD_ARTIFACT 在任何目录或媒体写入前的只读身份、代理、根目录检查失败，可以返回该次 completedEffects/pendingEffects 均为显式空数组的未开始证明；仅在 NEEDS_ACTION/RETRYABLE_ERROR 且非未知、中断、未确认停止类错误时允许用户显式 RETRY，保留原 code/event 和 operationId。effects 缺失、null、非空或执行状态未知仍先 RECONCILE。下载继续沿用已冻结的已知失败恢复规则，认证或代理恢复本身不重排业务任务。
