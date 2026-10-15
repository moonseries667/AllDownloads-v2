# 第一部分冻结契约（2026-10-15）

本文件是第一部分实现的增量规范，优先于旧 HTTP/Runner 文档的冲突表述。Java 对话单写，前端与 Go 读取本目录已提交版本。公共 API 仍为 `/api/v1`，内部 API 为 `/internal/v1`，统一 `{data,requestId}` / `{error:{code,message,retryable,action,details},requestId}`。字节数是十进制字符串，未知为 null。所有时间为 UTC；日期筛选使用上海时区。

机器结构见 [openapi.yaml](openapi.yaml)（JSON 格式的合法 YAML）与 [runner.schema.json](runner.schema.json)，共同样例见 [feedback1.examples.json](feedback1.examples.json)。仅冻结本轮 Bilibili 链路，不表示旧文档全部平台 API 已实现。

## 公共 API

| 路由 | 数据与规则 |
| --- | --- |
| GET `/health`、`/overview` | `runtime:{state,observedAt,services:[{id,required,state,reasonCode,lastSeenAt}],mount:{state,reasonCode,observedAt}}`；运行 state 为 RUNNING/PARTIAL/UNAVAILABLE/UNKNOWN，组件为 ACTIVE/LOST/ERROR/NOT_CONFIGURED/UNKNOWN；mount 为 MOUNTED/NOT_MOUNTED/NOT_CONFIGURED/UNKNOWN/UNSUPPORTED。Java 自身可证明活动；Go 须注册且最近 90 秒内心跳/领取。目录存在不证明挂载。overview 另含 `bilibili:{accountState,refreshState,downloadCount,archiveCount}`。 |
| GET `/bilibili/sources` | 分页 Source，新增 `pinned:boolean, navigationGroup:FAVORITES/COLLECTIONS/OTHER, displayLabel:string`。USER_ADDED、MOVIE、SERIES 在 OTHER，实际来源 id 和 kind 保留。 |
| PATCH `/bilibili/sources/{id}` | `{alias?,pinned?}`，If-Match 为带引号 revision；返回新 Source 与 ETag，冲突 VERSION_CHANGED。 |
| POST `/bilibili/sources` | `{links:[string]}`；返回 `{batchId,items:[{sourceId,taskId,reused,error}]}`，仅创建 SYNC_BIL_SOURCE 信息任务。 |
| POST `/bilibili/sources/{id}/refresh` | `{force:boolean}`，创建/复用信息任务。 |
| GET `/bilibili/contents` | `sourceId,q,type,state,sort,from,to,cursor,pageSize`；sort 为 TITLE_ASC/TITLE_DESC/PUBLISHED_DESC/PUBLISHED_ASC，默认 PUBLISHED_DESC。from/to 为 yyyy-MM-dd，from 当日 00:00 含、to 次日 00:00 不含；无发布时间在时间排序末尾，日期过滤时排除。标题按 Unicode 字符序；同值以 id 升序。游标绑定筛选/排序及目录 revision，变化返回 VERSION_CHANGED，不能混用分页。 |
| GET `/bilibili/contents/{id}` | ContentDetail（下表），全部有效来源和媒体树。 |
| GET `/bilibili/contents/{id}/parts` | 分页 MediaUnit；与详情的 mediaUnits 相同。 |
| POST `/download-targets/validate` | `{path:string}`，Idempotency-Key；只创建 VALIDATE_TARGET，202 `{targetId,taskId}`。path 是运行主机目录。 |
| GET `/download-targets/{id}` | `{id,revision,status,path,label,rootId,archivePolicy,error}`，status VALIDATING/VALID/INVALID；只有 VALID 才可预览。批次覆盖 archivePolicy=NONE。 |
| GET `/settings` | `downloadRoot,storageRevision,timezone,archiveDestinationLabel,defaultTarget`；defaultTarget 为受控目标，尚未由 Go 验证时不得假称可用。 |
| POST `/download-previews` | PreviewRequest；Idempotency-Key；202 `{previewId,taskId}`，本地目录资料足够时可直接 READY，taskId=null。 |
| GET `/download-previews/{id}` | Preview；每个 item 是一个 MediaUnit 的选择，含底层 outputs。 |
| GET `/download-previews/{id}/items` | items 分页，不重新计算选择。 |
| POST `/download-orders` | `{previewId}`，Idempotency-Key；202 `{orderId,taskIds,reused}`。绑定已展示的不可变预览，不接受附带新选择/路径。 |
| GET `/download-orders`、`/download-orders/{id}` | 分页/单个 Order：`id,previewId,status,target,archivePolicy,items,taskIds,createdAt`。 |
| GET `/tasks`、`/tasks/{id}` | 分页/单个 Task；状态、错误和允许动作真实。 |
| POST `/tasks/{id}/actions` | `{action:PAUSE/RESUME/CANCEL/RETRY/RECONCILE}`，If-Match；NEEDS_ACTION 的不确定副作用不能直接 RETRY。 |

ContentDetail 保留旧字段，新增 `revision,kind:SINGLE/MULTIPART/SERIES/MOVIE,externalIdType:BVID/SEASON_ID/EPISODE_ID/MEDIA_ID,publishedAt:null|string,author:null|{id,name},description,seasonId:null|string,sources:Source[],mediaUnits:MediaUnit[],warnings:string[]`。SERIES 的内容身份固定为一季，不跨季合并。`externalId` 是实际值，`originalUrl` 是实际平台内容地址。

MediaUnit：`id,contentId,externalId,title,index,kind:MAIN/PART/EPISODE/LANGUAGE,language:null|string,languageLabel:null|string,durationSeconds:null|number,availability,availabilityReason:null|string,tracks:MediaTrack[]`。电影的语言轨道共用同一视频关系，不能伪造 P。MediaTrack：`id,kind:VIDEO/AUDIO/SUBTITLE/DANMAKU,available:boolean,reasonCode:null|string,language:null|string,languageLabel:null|string,subtitleOrigin:HUMAN/AI/UNKNOWN/NOT_APPLICABLE,durationSeconds:null|number,sizeBytes:null|string`。AI/UNKNOWN 字幕不进入公共可选 tracks，warnings 说明未知分类；available=false 的真实缺项可显示原因但不可选择。track id 在同一内容内唯一稳定。公开模型不含播放 URL、Cookie 或会话。

PreviewRequest：`{platform:"BILIBILI",selection:{mode:"EXPLICIT"|"QUERY",resource:"BILIBILI_CONTENTS"|"BILIBILI_PARTS",ids?:string[],filters?:object,excludeIds?:string[],mediaSelections?:[{contentId,contentRevision,trackIds:string[]}]},target?:{id,revision}}`。没有 mediaSelections 中的某内容条目表示该内容全部可下载项；显式 trackIds=[] 返回 EMPTY_SELECTION，绝不恢复全选。默认与显式都在 Java 展开并冻结；Go 不自行全选。mediaSelections 不允许引用选择范围以外的内容或轨道。

Preview：`id,status,platform,expiresAt,storageRevision,target,archivePolicy,items,summary:{download,reuse,blocked,total},warnings`；target 是受控目标快照。item：`id,contentId,partId,title,version,relativePath,sizeBytes,decision:DOWNLOAD/REUSE/BLOCKED,reasonCode,reason,actionLabel,selectedTrackIds,mergePolicy:MUX/SEPARATE,outputs,downloadState,archiveState`。输出：`id,relativePath,trackIds,kind:VIDEO/AUDIO/MUXED/SUBTITLE/DANMAKU,sizeBytes`。大小未知 null，任何一个未知则总量未知。默认最高可用质量固定为 HIGHEST_AVAILABLE。

原因码：PENDING_DOWNLOAD、DOWNLOADING、LOCAL_VERIFIED、ARCHIVE_VERIFIED、RESOURCE_DELETED、PERMISSION_DENIED、REGION_RESTRICTED、PAYMENT_REQUIRED、NETWORK_TEMPORARY、STATE_UNKNOWN、PARTIAL_EXISTS、CUSTOM_TARGET_NO_ARCHIVE。REMOVED 必须来自明确删除证据；403/超时不能变成删除。REUSE 保留独立 downloadState/archiveState，不等于归档成功。仅部分选中成果存在不复用整项。覆盖目标独立去重，不因默认目录已归档而跳过。

预览有效十分钟，资源 revision、目标 revision、默认 storageRevision 变化时提交冲突。已提交订单不再受全局设置/新剧集影响。批次覆盖目标 archivePolicy=NONE，后续重试、恢复、结果重放不得创建 UPLOAD_ARTIFACT；默认目标为 AUTO，实际归档未实现/未配置时保持待处理，不能宣称 VERIFIED。

路径：`Bilibili/来源名称(来源类型)/标题(实际标识)/`，单视频无多余 P 层；多 P 为 `P01-副标题` 或 `P01`；番剧为实际集副标题，缺失为 `第01集(实际集标识)`；电影用实际版本标题/语言名，缺失为 `电影(实际标识)`。括号英文，Windows 安全化/长度及大小写碰撞由 Java 处理，持久路径映射不随标题刷新移动。具体媒体输出文件放在该目录中，视频音频同时选为 `media.mp4`，单选为 `video.mp4`/`audio.m4a`，字幕按语言+track 稳定标识命名，弹幕 `.xml`。

## Java–Go HTTP（protocolVersion=1，payload.schemaVersion=1）

内部调用仅回环，`Authorization: Bearer <内部运行凭据>`，由配置文件/环境注入，不进入源码/日志。公共路由禁止此身份冒充浏览器，内部路由拒绝浏览器 Origin。所有内部响应同样 envelope。时间戳、fencingToken 与字节规则同上。

| POST 路由 | 请求/响应 data |
| --- | --- |
| `/runners/register` | `{runnerId,protocolVersion:1,capabilities:string[],tools:object,facts?:{mount:{state,reasonCode,observedAt},accountState}}` → `{runnerId,protocolVersion:1,heartbeatSeconds:15,leaseSeconds:90}`。未支持能力不领取。 |
| `/leases/acquire` | `{runnerId,capabilities:string[]}` → `{operation:Operation|null}`，同时更新存活。 |
| `/leases/{leaseId}/heartbeat` | `{eventId,runnerId,operationId,attempt,fencingToken,sequence}` → `{desiredAction,leaseExpiresAt}`，过期返回 RECONCILE_REQUIRED。 |
| `/operations/{id}/facts` | `{eventId,runnerId,leaseId,attempt,fencingToken,sequence,facts:object}` → `{ackId,eventId}`，仅记事实、不终结任务。 |
| `/operations/{id}/result`、`/operations/{id}/reconcile` | `{eventId,runnerId,leaseId,attempt,fencingToken,status:SUCCEEDED/PAUSED/CANCELLED/RETRYABLE_ERROR/NEEDS_ACTION/RESULT_UNKNOWN,result:object|null,error:null|{code,message,retryAfterSeconds,completedEffects,pendingEffects}}` → `{ackId,eventId,taskId,status}`。同 event 同内容重放同 ack；改变内容 EVENT_CONFLICT。 |

Operation：`protocolVersion,operationId,taskId,attempt,leaseId,runnerId,fencingToken,leaseExpiresAt,operationType,platform,accountRef:null|string,resourceKey,storageRevision,destination:null|{rootId,rootPath,relativePath},payload`。payload 不含 shell/任意命令。Go 仅接受其根目录策略允许的 rootPath，并核对 rootId；每次执行重新检查实际路径及可写性。

本轮操作 payload / 成功 result：

* VALIDATE_TARGET：`{schemaVersion:1,targetId,path}` → `{targetId,rootId,canonicalPath,writable:true}`。Go 验证真实路径、本机属性、允许范围及可写性；rootId 是持久稳定的目标身份。失败必须具体说明，不创建下载。
* SYNC_BIL_SOURCE：`{schemaVersion:1,sourceId,kind,externalId,url}` → `{source:Source,contents:ContentDetail[],complete:boolean}`。完整结果事务发布；不完整不能移除成员。metadata 获取不下载媒体。Go 可在 result 中返回标准化公开元数据；私有 locator 留在 Go，不传给浏览器。
* FETCH_BIL_METADATA：`{schemaVersion:1,contentId,externalId,externalIdType}` → `{content:ContentDetail}`。
* DOWNLOAD_MEDIA：`{schemaVersion:1,contentId,contentRevision,contentKind,externalId,externalIdType,qualityPolicy:"HIGHEST_AVAILABLE",archivePolicy:"AUTO"|"NONE",units:MediaUnit[],outputs:Output[]}`，units.tracks 只含明确选择，outputs 明确合并关系与成品路径。destination.relativePath 是内容基目录，outputs.relativePath 是相对 rootPath 的完整路径。成功 `{files:[{outputId,relativePath,sizeBytes,sha256,mediaKinds:string[],durationSeconds:null|number,verified:true}],warnings:string[]}`；必须恰好覆盖所有 outputs，并核对媒体流。Go 先发布/验证并持久收据才报告。失败不可伪造完整结果。
* UPLOAD_ARTIFACT/VERIFY_ARCHIVE：沿用旧语义；本轮没有真实云端配置/能力则保持排队或 NEEDS_ACTION，不执行占位成功。

兼容：旧 fixture WRITE_FIXTURE/VERIFY_FIXTURE 不暴露到产品公共 API。旧前端无 mediaSelections 的请求仍默认全部可选项；新增枚举/模型由本轮前端同步消费。已有源站元数据与真实平台适配未验证时，公共返回 UNKNOWN/NEEDS_ACTION，不能用样例自动填充真实目录。
