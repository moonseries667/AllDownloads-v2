# Java 公共业务与持久调度

当前共同契约已通过 PR #29 合入主线 `0b34a82`。协调分支基于第三部分 `2c22e01`，再合入第一部分 Java `66979ac`、第二部分 Java `03107df`，三轮 Go 成果均在其祖先中；复用已有公共 HTTP、SQLite、租约与管理模型，补齐正式客户端所需追更及核对接缝。

第三部分契约为 [FEEDBACK-PART3.md](../../contracts/FEEDBACK-PART3.md)。`DownloadManagement` 在既有业务事务中维护提交批次、下载项目、可取消子项及唯一上传记录；`StorageSettings` 实现原生目录请求、Drive 目录分页、validate/save 和冻结目标。公开 `/download-projects` 用于管理，`/download-orders` 保留提交批次；归档页面只查询 `/archives`。

开工核验：B13 在前序仅有批次/执行任务，缺少管理实例；B14 已有任务级持久意图，缺少消费者与子项范围；B15 已有单操作完成后的上传入队，但不是项目完整性判断；B16 只有批次目录验证，无原生选择和全局保存；B17 仅有语义，无目录模型/实现。第三部分复用原 HTTP、SQLite、租约及输出占用，新增管理领域与控制路由，未另建 Runner。

`ManagementFlowTest` 覆盖精确 contentId/分页、共享消费者取消、运行停止确认、部分发布、完成竞争、失联对账、重启、显式新批次补齐与上传冻结；`StorageSettingsTest` 覆盖原生取消/不可用/迟到结果、设置冲突、Drive 重名/分页/权限变化；`ManagementHttpTest` 使用真实回环 HTTP 并生成 schema 校验样本。它们使用合成执行事实，不能替代实际 Go 文件执行或原生弹框。

第三部分 schema 复现：在项目隔离 Python 环境安装 `tests/integration/requirements.txt`，仓库根目录运行 `python tests/integration/validate_contracts.py backend/java/target/contract-captures.json backend/java/target/runner-capture.json backend/java/target/management-captures.json`。先执行下述 Maven verify 生成样本。原生 Windows 对话框、真实 Drive、Y7000P 部署与长期运行不在本轮已验收范围。
## 第二部分公共接入

本分支已合入第一部分 Java `a5dcded`、第二部分 Java `bb0ebdb` 和第三部分 Go `4995708`（含前序 Go）。`TelegramLibrary`、`QuarkCatalog` 与 `FeedbackService` 使用同一 BusinessStore 事务及 OperationQueue；Telegram 完整组资格、游标推进、照片成员、稳定问题和夸克转存/下载边界均保留。

第三部分将 Telegram 执行拆为单附件及侧车，保留完整消息/媒体组归属。取消不因增量扫描重启；用户显式补齐取消项时创建新操作和新项目实例，原实例不改写。夸克按单文件执行、按分享聚合，一个分享未完成不阻塞其他完整分享入上传。

Java 21 独占产品 SQLite、公共 HTTP、媒体选择和路径计划；Go 独占平台访问、目录探测与文件执行。第一部分契约提交为 `d508c7a`，见 [增量语义](../../contracts/FEEDBACK-PART1.md)、[OpenAPI](../../contracts/openapi.yaml) 和 [Runner schema](../../contracts/runner.schema.json)。旧 M1 `TaskStore` 与 fixture 测试保留，产品入口使用 `BusinessStore`/`OperationQueue`，不启动第二套队列。

## 开工核验与第一部分实现

基线 `bb9a1f2` 的 Java 只有内部 fixture 任务/租约/SQLite 测试；无公共 Controller、目录数据、预览订单和 HTTP Runner。Go 原本也只有文件 fixture 执行器。以下是逐项核验后补齐的范围，不代表真实账号已验收。

| 项目 | 基线核验 | 当前实现入口 |
| --- | --- | --- |
| B01 运行摘要 | 缺失 | `BusinessApplication.overview` 根据实际注册/存活/能力判断 Go；挂载与账号无观测即 UNKNOWN，不根据目录存在猜测 |
| B02 来源/排序 | 仅语义文档 | `CatalogService`：稳定来源、OTHER 导航、置顶 revision 持久化、四种全局排序、上海日期边界、绑定查询和目录版本的游标 |
| B03 详情媒体树 | 仅语义文档 | Go 规范化元数据经 `importContent` 保存，公开白名单字段，按季身份，排除 AI/未判明字幕，未知大小为 null |
| B04 选择快照 | 缺失；旧 fixture 请求不可复用为产品选择 | `DownloadService.preview/submit` 冻结轨道/输出/合并/质量/版本/目标；显式空选择错误，交叉选择输出占用去重 |
| B05 批次目录 | 仅旧单根规则 | VALIDATE_TARGET 经 Go 验证，目标引用持久化；NONE 在结果事务中禁止排上传，已提交订单固定目录 |
| B06 状态与动作 | fixture 状态机可参考 | 预览区别本地完成、云端核验、正在处理、部分存在、删除/权限/临时故障/未知，不把 REUSE 当归档 |
| B07 路径 | 仅旧命名文档 | `PathPlanner` 持久分配英文括号目录、多 P/季/电影规则及 Windows 安全化，刷新不搬迁 |
| B08 Go 执行 | 文件发布/收据基础已有 | 由 Go 对话实现；Java 只接受精确清单、大小、SHA-256、媒体种类和 verified 事实 |
| B09 跨进程 | 公共/内部 HTTP 均缺失 | `ApiController/RunnerController`、独立内部身份、租约/心跳/事实/结果/对账、持久订单和单事务后续调度 |

第一/二部分的契约与行为由其提交接续，第三部分没有重建公共 HTTP、租约或平台扫描系统。

## 账号与授权流程

`GET /api/v1/accounts` 返回固定 `bilibili-main`、`telegram-main`、`quark-main`、`drive-main` 的持久状态，初始为 UNKNOWN。只有用户主动 `POST /accounts/{id}/check` 才排 `CHECK_ACCOUNT`；Go 的实际检查结果才能写 ACTIVE。B 站来源同步、资料读取和媒体下载未指定账号时均绑定 `bilibili-main`。

授权步骤为带 Idempotency-Key 的 `POST /api/v1/auth-sessions {platform}`、只读 `GET /auth-sessions/{id}`、带新幂等键的 `POST /auth-sessions/{id}/steps {action:"POLL"}`。每个平台复用同一未结束流程；每次已经完成的 BEGIN/POLL 后，新的用户 POLL 创建不同 AUTH_STEP operation。`DELETE /auth-sessions/{id}` 必须提交 GET 的 ETag 作为 If-Match，立即保存 CANCELLED 并排 CANCEL，迟到成功不能复活。取消不会清除已保存账号或取消媒体任务。

Java 将流程上限固定为十分钟，Go 可以缩短；GET 不排操作也不写状态，五秒维护事务和启动恢复持久处理过期及清理。Java 重启沿用同一流程与操作；失联操作保留队列的 RESULT_UNKNOWN 核对语义，超过流程截止时间无法接受迟到成功。失败结果显示 NEEDS_ACTION/NONE 与明确原因，须取消后重新开始；LOCAL_SETUP 流程可在 Y7000P 配置后主动检查或 POLL。Telegram、夸克首次配置和缺少 Desktop OAuth client 的 Drive 依赖 Go 返回 LOCAL_SETUP，Java 不接收任何密码、验证码、Cookie 或 token。授权完成不会自动检查账号、扫描内容或重放未知上传。

`AccountsFlowTest` 与 `AccountsHttpTest` 使用隔离 SQLite、合成 Go 结果和真实回环 HTTP 验证四账号、幂等、官方 URL、版本取消、旧结果、过期、重启及单账号引用；不访问真实平台。Java 只保存允许的公共字段及自行生成的安全提示，授权结果去重仅保存摘要，避免把原始平台提示存入事件表。

Drive 明确报告 AUTH_CLIENT_REQUIRED 或 AUTH_SESSION_NOT_FOUND 时，公共流程显示 NEEDS_ACTION/LOCAL_SETUP 和中文配置原因；配置后可主动 POLL 或检查账号。内部 AccountFact/AuthSessionFact 可省略 Java 管理的 revision/taskId，空 expiresAt 使用 Java 的有界截止时间。跨 origin 取消支持 DELETE 预检及 If-Match，并向前端暴露 ETag；HTTP 测试捕获保存为 `target/accounts-captures.json` 供冻结 schema 核验。

## 上传核对与恢复

上传进入 NEEDS_ACTION 后，通过已有 `POST /tasks/{id}/actions {action:"RECONCILE"}` 与 If-Match 请求只读 VERIFY_ARCHIVE。Java 从原操作复制 accountRef、destination、storageRevision 及完整 upload payload；核对不创建新上传身份。完整结果经原 `uploadResult` 校验后确认原任务并解决对应问题；只有 safeToResume 才重排原 operationId，保留 PAUSE/CANCEL。其他观察继续 NEEDS_ACTION，核对结束或失败后可重新申请。旧核对结果绑定其申请时的原上传 attempt/fencing token，不能覆盖后来继续执行的上传。

来源/媒体元数据、账号检查、目录读取与校验等明确只读操作在 NEEDS_ACTION 支持用户显式 RETRY。上传失败不自动重发；只有 AUTH_REQUIRED/ACCOUNT_NOT_CONFIGURED 且该次回报明确含两个空 effects 数组，才开放显式 RETRY。RESULT_UNKNOWN、网络失败、缺失或非空 effects 继续先核对，不触发重新下载。`ArchiveRecoveryTest` 验证固定计划、取消/暂停、重复观察、跨重启、旧结果、事务回滚和上述重试边界；它使用合成 Go 事实，不代表真实 Drive 核对已验收。

## 构建与测试

锁定 Spring Boot 3.5.16、sqlite-jdbc 3.53.4.0、Maven Wrapper 3.3.4、Maven 3.9.16、Surefire 3.5.6。使用 Java 21，在本模块运行：

```powershell
.\mvnw.cmd --batch-mode --no-transfer-progress -DfailIfNoTests=true verify
```

Carbon 验证复用已预热的锁定 Maven/依赖缓存，使用隔离测试目录；离线构建添加 `-o -Dmaven.repo.local=<已预热的本地仓库>`，不使用旧 aliyun settings。JUnit 临时目录可通过 `-DargLine=-Djava.io.tmpdir=<工作树>/backend/java/target/junit-temp` 固定在工作区；既有强制终止子进程测试若遇到 shm 文件 AccessDenied/IOERR_TRUNCATE，需按执行边界在沙盒外复验，不能跳过该项。该主机路径不进入产品配置。Windows Wrapper 修复了普通目录 Target=null 时的空数组索引；未更换 Wrapper 或依赖版本。

`BusinessFlowTest` 验证第一部分业务、排序/日期/分页、置顶/重启、季身份、空选择、AI 排除、未知大小、目录/资源变更、精确去重和 NONE 的重试/重启/重放。`HttpBoundaryTest` 启动真实回环 Spring HTTP 与临时 SQLite，验证 envelope、幂等、ETag 和内部身份隔离。原 `TaskStoreTest/TaskStoreProcessTest` 保留原事务/进程恢复用例。业务单测使用合成 Go 事实，不当作真实文件或平台验收。

schema 校验（在仓库根目录，Python 项目隔离环境安装测试依赖）：

```powershell
python -m pip install -r tests/integration/requirements.txt
python tests/integration/validate_contracts.py backend/java/target/contract-captures.json backend/java/target/runner-capture.json
```

## 隔离启动与用户复现

复制 `application.example.properties` 为不入库的 `application.local.properties`，填入隔离数据库、私有 token 文件及实际存在的本机下载根；Go 的 allowedRoots 必须允许该目录。运行：

```powershell
java -jar target/alldownloads-backend-0.1.0-SNAPSHOT.jar --spring.config.additional-location=./application.local.properties
```

默认仅监听 127.0.0.1。第一次启动生成内部 token 文件；由 Go 启动环境读取供内部认证，不能放入浏览器、日志或命令参数。开发前端需要单独配置精确 allowed-origins 或使用同源代理。未配置目录/Go 时仍可浏览已有资料，但目录显示未通过验证，预览不会假成功。

用户流程：新增 Bilibili 来源 → Go 仅获取资料 → 浏览/排序/置顶 → 详情保存轨道选择（浏览器会话） → POST download-previews → GET READY 快照 → POST download-orders → 查询订单与任务。修改批次目录先 POST download-targets/validate 并轮询 VALID，再重建预览。提交只接受 previewId，不能在提交时悄悄替换路径/选择。

## 持久化与恢复

产品库使用 WAL、FULL 同步、3 秒 busy 超时、短事务和进程排他锁；来源/内容、目标、预览、订单、操作、输出占用和结果确认保存到同一数据库。幂等键绑定规范化请求，重复结果返回同一持久 ack。结果、文件事实和 AUTO 后续任务同事务提交；任何失败回滚，不返回持久成功。

90 秒租约、15 秒心跳；每次领取递增 attempt/fencing token。过期操作进入 NEEDS_ACTION，不自动重新发给其他执行者；保留旧证明供对账。仅明确无未确认副作用的失败按 30 秒/2 分钟/10 分钟重试，最多三次。用户暂停/取消先存意图，不能抹去已经发布文件的事实。NONE 不会因重试、Java 重启、结果重放或后续扫描进入上传。

旧 fixture 数据库与新 business.db 都不能当作旧 AllDownload 数据导入入口。当前 schema 为全新 v2 产品开发库版本 1；没有读旧账号、旧任务或业务媒体。

## 实质限制

Java 公共业务覆盖三部分反馈及 [正式客户端增量](../../contracts/PLATFORM-CLIENTS.md)。Go 正式命令已装配 Telegram/夸克客户端；Java 统一管理已启用的收藏监控、每小时夸克追更、手动刷新合并、固定目标及未知转存的只读核对。全部配置和账号授权仍由正式运行环境建立，选择能力不代表已经登录。Drive 上传已支持持久预留 ID 后创建必要相对目录，重名不唯一仍明确失败；目录选择与只读 Verify 不创建目录。

未执行原生 Windows 对话框的点击/视觉验收、真实 Bilibili/Telegram/夸克/Drive 账号及业务媒体、Y7000P 部署、电源断电或七天运行；本地跨进程通过不能替代这些结果。

正式客户端的跨进程入口见 [Go 复现步骤](../go/README.md#正式入口的隔离跨进程复现)。`PlatformClientFlowTest` 补充夸克一小时追更、关闭监控、活动请求合并、未知结果只读核对、原 operation 安全恢复与取消意图、facts 序号/原确认重放。`DomainBehaviorTest` 保留云端合法冒号名称，并由 Java 统一转义本机相对路径。它们使用同类型 SQLite，不读取旧业务数据。

## 第三部分跨进程复现

`tests/integration/feedback3_chain.py` 启动真实 Java jar、真实 Go Worker 和本地 HTTP 媒体/Drive 替身。它验证单部分取消实际关闭流、兄弟部分完成、未完整项目不入归档、固定清单上传核验，以及 Java/Go 重启不重复 I/O。运行数据与日志保留在 `backend/java/target/feedback3-chain-*`，不进入 Git；所有子进程有界结束，不修改系统配置。

先完成 Maven verify，在仓库根目录执行（路径替换为现有 Java 21、Go 1.27.1、FFmpeg 9.0.2）：

```powershell
# 构建集成测试宿主；其 go.mod 只通过 replace 使用当前工作树生产 Go 源码。
Push-Location tests/integration/go_runner
go build -o ../../../backend/java/target/feedback3-runner.exe .
Pop-Location
python tests/integration/feedback3_chain.py --java '<Java21>/bin/java.exe' --runner '<仓库>/backend/java/target/feedback3-runner.exe' --ffmpeg-dir '<FFmpeg9.0.2>/bin'
```

本轮实际结果为两项目、一取消部分、一已核验上传项目和两远端替身文件；取消 HTTP 在 8 MiB 流的约 128 KiB 处中断，兄弟文件保留，重启后下载/上传调用数量不增加。Go 全量 race/vet/build 已在该组合分支通过，设置了 `ALLDOWNLOADS_TEST_FFMPEG_DIR`，真实临时媒体测试未跳过。

第三部分交接时 Maven verify 为 64 项；当前协调组合的最终 verify 为 77 项全部通过（0 失败、0 错误、0 跳过）。共享 schema、HTTP 捕获通过。前端源码不在本后端分支，没有把前端 skipped 记为界面验收。

Refs [#2](https://github.com/moonseries667/AllDownloads-v2/issues/2)、[#4](https://github.com/moonseries667/AllDownloads-v2/issues/4)、[#5](https://github.com/moonseries667/AllDownloads-v2/issues/5)。
