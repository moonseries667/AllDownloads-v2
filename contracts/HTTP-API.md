# 前端与 Java 接口契约

正式平台装配与夸克追更/核对增量见 [平台客户端契约](PLATFORM-CLIENTS.md)；第三部分模型保持兼容。

第三部分下载项目、取消、归档队列与目录设置见 [第三部分冻结契约](FEEDBACK-PART3.md)。批次与下载项目使用不同稳定 ID。

第一部分反馈的增量结构、批次目标和媒体选择以 [第一部分冻结契约](FEEDBACK-PART1.md) 与 [OpenAPI](openapi.yaml) 为准。

## 1 契约范围

语义版本 `v1`，公共前缀 `/api/v1`，JSON UTF-8。浏览器只调用 Java。此文件冻结资源语义、关键字段和动作；Java 对话在首个开发阶段生成 `contracts/openapi.yaml`，覆盖本文的路由、模型、状态码及示例。前端可据本文先制作页面和 mock，生成式客户端及联调以审阅后的 OpenAPI 为准。不得在两端分别猜测字段。

正式公共接口由 Y7000P 上的 Java 提供，正式浏览器也在 Y7000P 使用。Carbon 开发代理只连接 mock 或隔离测试实例；部署阶段的真实联调按明确配置连接 Y7000P。“本机文件”和 open-local 动作始终指 API 运行主机 Y7000P 的文件，不能解析为 Carbon 开发目录。

响应对象使用 camelCase；ID、平台大整数和字节计数使用十进制字符串或不透明字符串，普通页容量与小数量用整数。时间是 UTC RFC3339，显示时区 `Asia/Shanghai`；目录日期由后端给出，前端不得重新拼路径。未知值为 null，不用 0 或空字符串冒充已知事实。

统一外壳：成功 `{data, requestId}`；失败 `{error:{code,message,retryable,action,details},requestId}`。`details` 仅含脱敏的字段或对象问题。列表 `data={items,nextCursor,total}`，total 不可确定时为 null；默认 pageSize=50，最大 200。筛选稳定排序必须包含 ID 作为最终顺序依据。

所有创建任务的 POST 使用 `Idempotency-Key`。同一键和相同规范化请求重复提交返回同一资源；同一键但内容不同返回 409。用户重复操作的业务去重独立于该键存在。接收成功 202 表示已持久化，不表示已执行；不能将长下载保持在原浏览器请求里。

## 2 基础模型

| 模型 | 必需字段及语义 |
| --- | --- |
| Platform | `BILIBILI / TELEGRAM / QUARK` |
| Source | `id,platform,kind,title,alias,url,status,lastSyncedAt,itemCount,revision`；kind 为 FAVORITE/COLLECTION/USER_ADDED/SERIES/MOVIE |
| Content | `id,platform,externalId,title,coverUrl,sourceIds,originalUrl,availability,downloadSummary,archiveSummary,updatedAt` |
| MediaPart | `id,contentId,externalId,title,index,kind,durationSeconds,availability`；kind 为 PART/EPISODE/MAIN |
| Message | `id,externalId,sentAt,text,attachments,groupId,sourceLinks,downloadSummary`；附件含 `id,name,mediaType,sizeBytes,state` |
| TelegramResource | `id,accountId,primaryMessageId,messageIds,mediaGroupId,text,sourceLinks,authorTags,channel,mediaCount,completedCount,failedCount,attachments,directoryLabel,state`；一个资源帖或媒体组，而非整个链接提交批次 |
| Attachment | `id,messageId,externalId,name,mediaType,sizeBytes,downloadState,artifactId,relativePath,metadataRelativePath,error`；未下载时文件位置可为 null，下载状态为 NOT_REQUESTED/QUEUED/DOWNLOADING/VERIFIED/NEEDS_ACTION |
| SavedScanState | `accountId,cursorMessageId,scannedThroughAt,initialScanCompleted,lastScanAt,activeTaskId`；游标为最后完整收录的消息 ID，不表示附件全部下载完成 |
| Share | `id,accountId,externalShareId,title,targetFolderId,targetPath,shareFolderKey,watchEnabled,updateState,lastCheckedAt,nextCheckAt,revision`；externalShareId 是平台完整分享标识 |
| CloudNode | `id,shareId,parentId,externalId,version,name,kind,relativePath,sizeBytes,modifiedAt,availability,downloadSummary`；shareId 引用内部 Share.id，kind 为 FILE/FOLDER |
| Artifact | `id,platform,contentId,relativeDirectory,storageRevision,files,localState,archiveState,warnings`；files 含相对路径、类型、大小及验证状态 |
| Task | `id,platform,kind,status,phase,progress,desiredAction,allowedActions,error,nextRetryAt,createdAt,updatedAt,revision`；desiredAction 为 CONTINUE/PAUSE/CANCEL，用于表达已受理但尚未完成的控制意图 |
| Progress | `doneItems,totalItems,doneBytes,totalBytes,bytesPerSecond`；未知总数为 null |
| Account | `id,platform,label,state,capabilities,lastCheckedAt`；不含凭据 |

`availability` 为 AVAILABLE/REMOVED/UNAVAILABLE/UNKNOWN，原因单独放 `availabilityReason`。`downloadSummary` 包含 state、已完成/总部分数、关联 orderIds；`archiveSummary` 包含 state、已核对/总文件数及 archiveIds。接口不以一个总 status 混合信息、下载、存档和转存。

Task.kind 为 INFO_SYNC/PREVIEW/TRANSFER_SYNC/DOWNLOAD/UPLOAD/VERIFY/AUTH。Task.status 为 QUEUED/RUNNING/PAUSING/PAUSED/WAITING_RETRY/NEEDS_ACTION/SUCCEEDED/FAILED/CANCELLED，批次聚合允许 PARTIAL。`allowedActions` 是 PAUSE/RESUME/CANCEL/RETRY/RECONCILE 的子集，页面按实际能力显示。

`localState` 为 MISSING/PARTIAL/VERIFIED/CLEANED；`archiveState` 为 NOT_REQUESTED/QUEUED/UPLOADING/VERIFYING/VERIFIED/NEEDS_ACTION。只有 VERIFIED 才能展示云端归档完成。

## 3 信息与来源接口

| 方法和路径 | 输入 | 结果及副作用 |
| --- | --- | --- |
| GET `/overview` | 无 | 平台入口、任务和异常数量 |
| GET `/search` | q、platform、cursor | 已收录标题或文件名匹配结果，含跳转资源信息 |
| GET `/bilibili/sources` | 常规分页 | Source 列表 |
| POST `/bilibili/sources` | `{links:[string]}` | 202，逐条创建信息解析任务，返回批次和条目 taskId；不下载 |
| PATCH `/bilibili/sources/{id}` | alias、pinned | 修改显示属性 |
| DELETE `/bilibili/sources/{id}` | 版本前置条件 | 按产品规则移除来源关系，保留相关归档与其他引用 |
| POST `/bilibili/sources/{id}/refresh` | `{force:boolean}` | 202，创建或复用信息任务，不下载 |
| GET `/bilibili/contents` | sourceId、q、type、state、sort、cursor | Content 列表 |
| GET `/bilibili/contents/{id}` | 无 | 完整信息、来源关系、警告 |
| GET `/bilibili/contents/{id}/parts` | 季、cursor | MediaPart 列表 |

来源重复添加返回已有 sourceId 及 reused=true。批量输入的语法错误可以逐条返回；合法项目任务持久化后返回，不因一个坏链接丢掉其他项目。来源删除不接收隐含 deleteFiles 参数。

## 4 Telegram 接口

| 方法和路径 | 输入 | 结果及副作用 |
| --- | --- | --- |
| GET `/telegram/messages` | q、mediaType、from、to、cursor | 已收录 Message 列表，可搜索正文 |
| GET `/telegram/messages/{id}` | 无 | 完整文字、附件、来源及下载记录 |
| POST `/telegram/saved-sync` | `{accountId}` | 202，原有“下载收藏增量”：扫描后下载目标 pending 媒体 |
| GET `/telegram/watcher` | accountId | enabled、intervalSeconds、lastCheckedAt、nextCheckAt，以及 scanState: SavedScanState |
| PATCH `/telegram/watcher` | `{accountId,enabled}` | 默认周期 60 秒；未完成首次扫描时启用返回 INITIAL_SCAN_REQUIRED |
| POST `/telegram/link-downloads` | 旧入口 | 410 FEATURE_REMOVED；收藏夹是唯一产品下载入口 |
| GET `/telegram/resources`、GET `/telegram/resources/{id}` | q、state、cursor | 分别返回分页 TelegramResource 和单个 TelegramResource；附件状态独立 |
| GET `/telegram/resources/{id}/metadata` | 无 | `{schemaVersion,resource,messages,files}`；resource 为 TelegramResource，messages 为 Message 数组，files 含附件标识、文件相对位置和对应侧车对象 |
| POST `/telegram/retry-incomplete` | `{accountId,resourceIds?:[string]}` | 202，补齐所选或全部未完成的目标附件 |

首次 saved-sync 扫描全部可访问历史并下载符合音视频资格的完整单元。普通 GET 和启动后查询 watcher 不访问 Telegram 发起扫描；监控开关与任务暂停分别管理。消息链接提交返回 FEATURE_REMOVED。

Message.attachments 和 TelegramResource.attachments 均使用 Attachment。每个附件的 metadataRelativePath 是其完整文件名后追加 `.metadata.json` 的同级文件；Artifact 清单同时登记媒体与侧车，资源导出只汇总，不替代这些逐附件侧车。未知 channel、authorTags 等允许 null 或空数组，不能猜来源。scannedThroughAt 表示游标对应消息时间；扫描进度由活动 Task.progress 提供。

## 5 夸克接口

| 方法和路径 | 输入 | 结果及副作用 |
| --- | --- | --- |
| GET `/quark/shares` | cursor | 分享与追更列表 |
| GET `/quark/folders` | accountId、parentId、cursor | 账号目录选择器，只返回目录信息 |
| POST `/quark/folders` | `{accountId,parentId,name}` | 202，新建夸克目标目录，返回 taskId |
| POST `/quark/shares` | `{accountId,entries:[{url,targetFolderId}]}` | 202，批量转存并建立追更，返回 batchId 与每条 shareId/taskId/error |
| PATCH `/quark/shares/{id}` | `{watchEnabled}` | 启停该分享追更，不涉及下载 |
| POST `/quark/shares/{id}/refresh` | 无 | 202，检查并转存新增内容，不下载 |
| GET `/quark/shares/{id}` | 无 | 完整分享及当前转存结果 |
| GET `/quark/nodes` | shareId、parentId、q、sort、cursor | CloudNode 文件树的已收录视图 |
| POST `/quark/shares/{id}/refresh-tree` | 无 | 202，重新读取实际网盘节点，不创建下载 |

分享链接里的提取码由后端安全处理，不返回到默认日志和错误摘要。GET 列表可以返回缓存时间及 stale 标识，不通过读取请求隐式发起云盘写操作。

## 6 用户选择 下载和归档接口

`POST /download-previews` 输入：

```json
{
  "platform": "QUARK",
  "selection": {
    "mode": "EXPLICIT",
    "resource": "QUARK_NODES",
    "ids": ["node-example-1", "node-example-2"]
  }
}
```

selection.mode 为 EXPLICIT 或 QUERY。resource 为 BILIBILI_CONTENTS/BILIBILI_PARTS/TELEGRAM_ATTACHMENTS/QUARK_NODES；EXPLICIT 给 ids，QUERY 给该资源允许的 filters 和 excludeIds。禁止将任意表达式、SQL、绝对路径、下载 URL 当作选择参数。

返回 202 `{previewId,taskId}`，后端展开全部范围并固化文件版本。`GET /download-previews/{id}` 返回 `{id,status,platform,expiresAt,storageRevision,items,summary,warnings}`；status 为 PREPARING/READY/EXPIRED/NEEDS_ACTION。每个 item 含 contentId/partId/nodeId 中适用标识、version、relativePath、sizeBytes、decision 和 reason；decision 为 DOWNLOAD/REUSE/BLOCKED。items 较多时另用 `/download-previews/{id}/items` 分页读取。预览本身不下载。

预览设计默认有效十分钟。新增文件不使已固化文件集合自动扩大；所选文件变更、权限改变或 storageRevision 改变时，提交返回相应冲突并要求重新预览。

`POST /download-orders` 输入 `{previewId}`，返回 202 `{orderId,taskIds,reused}`。触发来源由服务器按此用户动作记录为 USER，不信任客户端自由传入 trigger。同一资源同一版本已在执行时复用任务。没有选择、预览未就绪或全部被阻止时不得返回虚假成功。

| 方法和路径 | 用途 |
| --- | --- |
| GET `/download-orders` | 按平台、来源、日期、状态查询订单及完成数量 |
| GET `/download-orders/{id}` | 查询订单快照、文件任务和实际目录 |
| GET `/artifacts` | 查询本机成果及独立的云端归档状态 |
| GET `/artifacts/{id}` | 文件清单、必要/补充信息、实际位置、警告 |
| POST `/artifacts/{id}/archive` | 创建或复用上传归档任务，不能重下平台媒体 |
| POST `/artifacts/{id}/verify` | 重新核对本机或云端成果 |
| POST `/artifacts/{id}/open-local` | 仅打开该成果已经登记且存在的本机目录，不接收任意路径 |
| POST `/cleanup-previews` | 预览已验证云端成果的本机副本清理范围 |
| POST `/cleanup-orders` | 提交清理预览，重新核对资格，不接受任意目录删除 |

归档可由已完成下载按产品默认规则自动入队；该动作不能反向创建下载。云端访问地址只在有真实云端对象时返回；没有可用 URL 时显示路径和原因，不能构造伪链接。

## 7 任务 设置和账号接口

| 方法和路径 | 主要数据 |
| --- | --- |
| GET `/tasks`、GET `/tasks/{id}` | Task、阶段明细、关联对象 |
| POST `/tasks/{id}/actions` | `{action}`，只接受当前 allowedActions；返回已记录的操作意图 |
| GET `/issues`、POST `/issues/{id}/acknowledge` | 可追溯异常与已读状态；已读不等于修复 |
| GET `/settings` | downloadRoot、archiveDestinationLabel、timezone、storageRevision、运行配额 |
| POST `/settings/validate` | 验证目标设置并返回问题及影响，不修改现有设置 |
| PATCH `/settings` | 基于 revision 的设置修改；冲突返回 409 |
| GET `/accounts`、POST `/accounts/{id}/check` | 账号状态查询与主动检查 |
| POST `/auth-sessions` | `{platform}`，建立用户登录流程，返回 authSessionId、nextAction、expiresAt |
| GET `/auth-sessions/{id}` | 当前状态与官方授权步骤提示 |
| POST `/auth-sessions/{id}/steps` | 按 nextAction 提交必要输入，敏感内容不持久到日志 |
| DELETE `/auth-sessions/{id}` | 取消本次登录流程 |
| GET `/health` | Java、Go、存储及配置的可用性摘要 |

暂停/恢复/取消返回的是意图受理结果，终态仍通过 Task 查询。重试只作用于失败阶段；RESULT_UNKNOWN 使用 RECONCILE，不能混同于无条件重做。

## 8 错误码和兼容

| 错误码 | HTTP | 用户可见行为 |
| --- | --- | --- |
| INVALID_INPUT / EMPTY_SELECTION | 400 | 指出输入或选择问题 |
| AUTH_REQUIRED | 401 | 登录入口，保留已有选择 |
| ACCOUNT_PERMISSION_DENIED | 403 | 解释账号无相应权限 |
| NOT_FOUND | 404 | 对象已经不存在或未收录 |
| IDEMPOTENCY_CONFLICT / VERSION_CHANGED / STORAGE_CHANGED | 409 | 重查或重新预览，不自动扩大选择 |
| INITIAL_SCAN_REQUIRED | 409 | 先执行首次收藏同步 |
| ACTION_NOT_ALLOWED | 409 | 刷新任务能力与状态 |
| INSUFFICIENT_SPACE / INVALID_STORAGE_ROOT | 422 | 修改或释放存储后继续 |
| RATE_LIMITED | 429 | 展示 nextRetryAt，不循环提交 |
| RUNNER_UNAVAILABLE | 503 | 保留浏览与选择，提示执行程序不可用 |
| RESULT_UNKNOWN / VERIFICATION_FAILED / PROVIDER_CHANGED | 作为任务错误 | 展示已知结果与核对、补齐或等待修复入口 |

HTTP 错误用于请求无法受理；后台执行失败写入 Task.error，不能通过轮询 HTTP 200 掩盖任务失败。兼容的新增字段可以增加，删除字段、改变枚举含义或自动化边界必须由协调对话修改契约并通知所有开发对话。

可编辑资源的 revision 是不透明字符串，响应提供对应 ETag。PATCH 和 DELETE 使用 If-Match 提交最近读到的版本；不匹配返回 VERSION_CHANGED。任务动作同样核对当前状态及 revision，拒绝过期界面上的无效操作。任务状态和 desiredAction 分别表达实际阶段与控制意图。
