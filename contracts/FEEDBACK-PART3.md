# 第三部分冻结契约：下载项目、上传队列与目录设置

本文件在 v1 上补充 [第一部分](FEEDBACK-PART1.md) 和 [第二部分](FEEDBACK-PART2.md)。浏览器只访问 Java。机器模型见 `management.schema.json`；运行操作仍使用 `runner.schema.json` 的 Operation/Result 外壳。新增操作在 Go 注册能力后才可领取。原 Task 状态与 desiredAction 不改名、不增加 CANCELLING 状态。

## 下载身份与查询

`POST /download-orders` 仍提交 previewId，返回 orderId（提交批次）及 taskIds，新增 projectIds。一次批次可以有多个下载项目。原 `/download-orders/{orderId}` 保留批次快照并增加 projects；不将批次 ID 当项目 ID。

`GET /download-projects?platform=&contentId=&cursor=&pageSize=` 返回标准分页。platform 可为 BILIBILI/TELEGRAM/QUARK；contentId 精确匹配稳定业务内容 ID，不使用标题模糊匹配。`GET /download-projects/{id}` 返回完整 DownloadProject。`GET /download-orders` 同时支持 platform/contentId 过滤，返回匹配的批次，不遗漏同内容多次订单。

DownloadProject 字段：`id,batchId,platform,contentId,title,revision,status,desiredAction,allowedActions,actionReason,createdAt,target,archivePolicy,archiveTarget,summary,items`。id 是下载实例，batchId 是原 orderId（Telegram 为收录/补齐批次）；contentId 分别为内容、第二部分逻辑收藏资源、分享 ID。id 不因同名、刷新或状态变化改变。archiveTarget 为提交时的 Drive 目标快照或 null。

DownloadChild 字段：`id,projectId,contentId,partId,attachmentId,nodeId,title,revision,status,desiredAction,allowedActions,actionReason,taskIds,outputs,files`。不适用的平台标识为 null。Bilibili 一个媒体部分/语言单元为一个子项，视频音频合并在该子项内一起取消；Telegram 一个附件连同其 metadata 侧车为一个子项；夸克一个实际文件为一个子项。outputs 是原冻结清单，files 是已验证事实。取消不得删除 outputs 或减少 summary.total。

summary：`total,completed,running,failed,cancelled,pending`，每个子项只计一次。status 使用原 Task 状态及聚合 PARTIAL；混合成功/取消等终态为 PARTIAL，全部取消才 CANCELLED，所有必要子项完成才 SUCCEEDED。desiredAction=CANCEL 且仍有运行/待核对子项时显示“正在取消”。每层 revision 为不透明字符串，状态或控制范围变化会改变；前端提交 If-Match（带引号）。

## 取消

* `POST /download-projects/{id}/actions {action:"CANCEL"}`
* `POST /download-projects/{id}/items/{itemId}/actions {action:"CANCEL"}`

两者需要 If-Match 和 Idempotency-Key，202 返回当前项目/子项模型。相同幂等请求返回原受理结果；键改变时按最新 revision 校验，冲突 VERSION_CHANGED；终态完成请求返回实际状态，不能改成取消。已请求取消的同目标可幂等返回。禁止通过项目取消上传。

排队且未领取的执行可由 Java 确认不会启动，直接 CANCELLED；运行中只持久化 CANCEL 意图，等待 Go 终态收据。失联执行仍 NEEDS_ACTION + CANCEL，不可假称已停止。完成竞争保留已发生的 SUCCEEDED 与文件事实，控制意图不反向删除成品。

Java 按上述子项发出独立 DOWNLOAD_MEDIA 操作：Bilibili 单一 unit，Telegram 单一 attachment 及侧车（messageIds/metadata 保留完整逻辑归属），夸克单一 node。现有 payload 结构不变。Go 取消当前操作及其网络、文件和合并子进程，不取消其他 operation。共享物理执行有多个活跃消费者时，仅取消该项目子项消费者，最后一个消费者取消才向 Go 发 CANCEL；未停止的共享执行不能声明已停止，但该消费者可标记取消并保留共享说明。

Go CANCELLED/NEEDS_ACTION 允许 result.files 返回已经发布并核验的冻结 outputs 子集；Java 验证每项身份、路径、大小、校验和，不将它解释为全量成功。SUCCEEDED 必须完整覆盖本操作 outputs。部分发布后取消仍不满足原项目完整性。重启及自动重试保留意图；Telegram 重复增量扫描不得恢复取消项。显式“补齐未完成”可创建新批次，原取消实例保持历史事实。

## 上传管理

`GET /archives?platform=&status=&cursor=&pageSize=` 只返回 ArchiveUpload：`id,projectId,batchId,platform,title,status,revision,desiredAction,allowedActions,progress,target,files,taskId,createdAt,error`。status/allowedActions 来自真实上传 Task；成功意味着远端清单已核验。操作复用 `POST /tasks/{taskId}/actions`，不提供无执行能力的按钮。

原项目的 archivePolicy=AUTO、全部原冻结必要 outputs 本机 verified、没有取消/失败/阻止/待核对子项，并且提交时已冻结有效 Drive 目标，才原子创建唯一上传任务和 ArchiveUpload。NONE 项目永不入队；旧项目没有 Drive 目标保持待配置问题，不从新全局设置吸收目标。每个独立项目自己判断资格。恢复扫描与结果重放复用 projectId 的唯一上传身份。

多个逻辑实例复用完全相同的本机成果、冻结文件清单及 Drive 目标时，持久关联同一上传记录，不重复上传实体；取消一个消费者不能取消其他实例依赖的执行。Telegram 自动收录批次的订单 previewId=null；夸克 PreviewItem 可带 shareId/projectTitle 供明确分享归属，下载项目 contentId 为 shareId。

UPLOAD_ARTIFACT payload：`{schemaVersion:1,projectId,archivePolicy:"AUTO",archiveTarget:DriveTarget,files:[ArtifactFile]}`，destination 为冻结的本机目标；ArtifactFile：`outputId,relativePath,sizeBytes,sha256,mediaKinds,verified:true`。仅传该项目不可变且已核验的实际成果，不能枚举目录后扩大上传。成功 result：`{projectId,archiveTarget:DriveTarget,files:[ArchivedFile],verified:true}`；ArchivedFile 包含上述 outputId/relativePath/sizeBytes/sha256，另含 `remoteId,remoteName,verified:true`。remoteId 为真实稳定对象，名称/大小/校验值匹配预期才成功。上传自行传输后核验，失败重试同操作，不重新下载源媒体。

## 全局设置

GET /settings 保留原字段，增加 `revision,archiveDestination:DriveTarget|null,directoryPicker:{available,reasonCode,hostId}`。hostId 是运行主机不透明身份，不是浏览器主机路径。默认配置持久保存后不能被启动参数覆盖。

`POST /settings/validate {downloadTarget:{id,revision},archiveDestination:{accountId,folderId,revision}|null}`（Idempotency-Key）返回 202 `{validationId,taskId}`。下载目标必须来自已验证受控 target；Drive 非空时创建 VALIDATE_DRIVE_TARGET。`GET /settings/validations/{id}` 返回 `{id,revision,status:VALIDATING/VALID/INVALID,downloadTarget,archiveDestination,error}`。`PATCH /settings {validationId}`（If-Match）仅接受 VALID 且基于当前 settings revision 的快照，原子增加 revision/storageRevision；选取/校验均不保存设置。

## 原生运行主机目录选择

`POST /directory-selections {}`（Idempotency-Key）仅由点击触发，202 `{selectionId,taskId}`。`GET /directory-selections/{id}` 返回 `{id,status:PENDING/SELECTED/CANCELLED/UNAVAILABLE,targetId:null|string,error:null|object}`。`POST /directory-selections/{id}/cancel {}` 取消待选取请求；取消不改设置。不能传浏览器文件句柄或客户端绝对路径。

Go 注册 facts.directoryPicker：`{available:boolean,reasonCode:null|string,hostId:string}`。非同机交互环境、无桌面或未配置能力必须 unavailable；Java 在不可用时返回明确状态，不入队反复弹窗。公共 API 只允许回环浏览器会话触发；宿主是否可交互由 Go 真实检测。

SELECT_LOCAL_DIRECTORY payload：`{schemaVersion:1,selectionId,title:"选择本机下载目录",timeoutSeconds:120}`，destination=null。成功 result 为 `{selectionId,cancelled:boolean,target:null|{rootId,canonicalPath,writable:true}}`。Go 调用运行主机原生文件夹对话框，有界等待、可取消；选择后验证真实本机目录与写入资格。cancelled=true 时 target=null；Java 生成受控 target（archivePolicy=NONE，供校验全局设置时转换 AUTO），选择本身不改默认值。非交互能力返回 NATIVE_PICKER_UNAVAILABLE；未实际弹框验收不可声称已完成。

## Google Drive 目录

`GET /drive/folders?accountId=&parentId=&cursor=&pageSize=` 返回 `{items:DriveFolder[],nextCursor,total,parent:DriveFolder|null,breadcrumbs:DriveFolder[],status:LOADING/READY/NEEDS_ACTION,taskId,error}`。只列目录，根 parentId=null，由 Go 解析真实 root；不虚构共享盘支持。缺快照创建一次 LIST_DRIVE_FOLDERS，浏览器轮询当前页面；cursor 不冒充目录 ID。

DriveFolder：`id,accountId,parentId,name,kind:"FOLDER",revision,selectable,availability,availabilityReason,path`，与 Quark 相同展示形状但数据/账号完全隔离。DriveTarget：`accountId,folderId,revision,name,path`，path 仅显示，执行按稳定 folderId。重名目录以 ID 区分，目标不可用不能回退 root。

LIST_DRIVE_FOLDERS payload：`{schemaVersion:1,accountId,parentId:null|string,cursor:null|string,pageSize:50}`；成功 result `{accountId,parentId,items:DriveFolder[],parent:DriveFolder,breadcrumbs:DriveFolder[],nextCursor:null|string}`。父链真实完整，nextCursor 是平台分页令牌；Java 将其包装为当前账号和父目录范围的受控 cursor。

VALIDATE_DRIVE_TARGET payload：`{schemaVersion:1,validationId,accountId,folderId,revision}`；成功 `{validationId,target:DriveTarget,writable:true}`。Go 根据实际身份、当前父链、权限复核，revision 变化返回 TARGET_CHANGED，删除 TARGET_NOT_FOUND，权限 TARGET_PERMISSION_DENIED，认证 AUTH_REQUIRED，分页 INVALID_CURSOR。合法目录与上传核验是不同事实。

显示名称按用户要求为“Goole Drive上传目录”；内部 provider/API 仍为 Google Drive。目录选择及设置校验不创建、删除、移动或重命名云端目录；上传所需中间目录的持久身份与恢复见 [平台客户端增量](PLATFORM-CLIENTS.md)。真实账号授权单独进行。
