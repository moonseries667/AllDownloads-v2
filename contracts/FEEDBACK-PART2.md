# 第二部分实现契约补充

基线为 `6233234` 中 [第二部分增量](FEEDBACK-PART1.md#第二部分增量同一-v1-协议)。本文件明确完整组、根目录和错误恢复的边界，不创建另一套 API。既有字段和第一部分模型继续适用。共同输入样例见 [feedback2.examples.json](feedback2.examples.json)。

扫描、目录、分享请求与结果的精确 payload 结构见 [feedback2.schema.json](feedback2.schema.json)，引用已有 runner schema 的共享模型；不把根协议中允许任意对象的占位结构当作字段校验。

## Telegram

- `externalId` 为消息的正十进制 ID，`afterMessageId`、`scannedThroughMessageId` 为非负十进制 ID，按数值比较，不按字符串排序。附件 `externalId` 在所属消息内稳定；业务身份由账号、消息、附件共同限定。单消息 `groupId=null` 且 `groupComplete=true`。
- `groupComplete=true` 是适配器已收集该平台组全部成员的事实。返回完整组时，同一结果必须携带其全部成员，所有成员标记 true；先前不完整页面允许重复返回这些成员。Java 按账号和稳定 groupId 合并，只有完整组才决定资格。不能只把最后一条标记 true、让接收方猜测还有多少成员。
- `complete=false` 必须携带非空 continuation；`complete=true` 必须 continuation=null。scannedThroughMessageId 是实际遍历边界；完整组补收成员允许超过该边界。确认游标不能超过边界或任何尚未完整组的最早成员减一。
- `complete=true` 但组仍不完整时，保留 pending 事实，初扫不算完成；返回需要核验的问题，不立即循环重扫。用户再次增量同步从确认游标之后补扫。没有合格新增是正常完成。
- `mediaType=VIDEO/AUDIO` 或真实 `mimeType` 为 `video/*`、`audio/*` 可建立资格；UNKNOWN 且无可靠 MIME 不猜测。Go 不返回缩略图为独立附件。合格单元保留所有实际附件，UNKNOWN 附件作为 DOCUMENT 输出，不能丢弃。
- 完成事实必须匹配冻结 outputs，每个附件和它的 METADATA 侧车均 verified=true，大小非负且校验值有效。失败或缺少照片/侧车不能将整个资源标记完成。重复扫描保持稳定资源、附件、任务及目录身份。
- 旧 link-downloads 返回 410 FEATURE_REMOVED；手动预览不接受 TELEGRAM 平台/附件资源。已保留历史记录不删除。

## 夸克

- `parentId=null` 的首次目录查询表示解析实际根节点。LIST_QUARK_TREE 的 folders 至少含实际根节点（其 parentId=null）和返回的直属子目录；非根查询可同时携带父节点和其祖先，便于核验父链。只把查询父节点的直属子项视为该页枚举成员。
- 根 title 为“我的云盘”，其展示 path 为 `""`。根本身可以选择；非根 path 从真实父链拼接，不含根标题。节点 name 为单个名称；包含路径分隔符的名称拒绝为 PROVIDER_CHANGED，不能自动裁剪伪造节点。
- 未有父链/完整快照时返回 LOADING 或 NEEDS_ACTION，不能将未知当空目录。完整枚举才可将该父节点原有但本次缺失的子节点标记 REMOVED；不会删除文件或历史任务。权限/移动变化由新 revision 表达；旧选择返回 VERSION_CHANGED 或 TARGET_UNAVAILABLE。
- shares 请求仅接受每条 entries 的 targetFolderId 与 targetFolderRevision，统一模式由前端展开；同名不同 id 保持独立。目标快照包含 accountId、id、revision、name、path；提交及实际转存都复核目标。无有效目标不能静默回退根目录。
- QUARK_NODES 的目录展开必须基于完整分享树；不完整返回 SNAPSHOT_INCOMPLETE。之后新增节点不扩大已有预览，已选文件版本变化仍须刷新。真实大小 null 与 `"0"` 区分。

## 通知

问题身份固定为所属任务和错误码；无任务的问题由稳定对象和错误码限定。同一问题重报更新 updatedAt，保留首次 occurredAt；重复轮询不写记录。acknowledge 只置 read=true。任务真正成功后解决其错误，成功结果附带的未解决 warning 保留；重试开始和关闭提示均不能解决问题。

公开错误 message 使用受控中文原因，不直接返回平台日志、任意错误堆栈或下载地址。GET /issues 默认 resolved=false，允许 `includeResolved=true` 查历史；稳定分页以 occurredAt 降序及 id 排序。
