# Java 公共业务与持久调度

## 第二部分接口与业务

契约基线 `6233234`，完整组与目标边界补充 `9e7886f`。第二部分共享内核接入 `bb0ebdb` 消费第一部分 Java `a5dcded` 和 Go 基线；Go 平台模块 `81c2080` 与绑定 `aff9ac8` 已在本分支接入。精确结构见 [第二部分契约](../../contracts/FEEDBACK-PART2.md)、[payload schema](../../contracts/feedback2.schema.json) 及公共 OpenAPI。

开工核验 B10/B11/B12 均只有契约，未有 Java 平台业务。现有 `FeedbackController`、`FeedbackService`、`TelegramLibrary`、`QuarkCatalog` 接入唯一 BusinessStore/OperationQueue；通知复用第一部分 `objects/issue`，未建立第二套通知数据。普通构造/收藏 GET 不扫描，监控只处理用户已启用且完成初扫的账号。收藏资格在完整组上统一计算，全部实际附件及侧车必须有事实才能完成；失败部分、游标、已读状态和固定路径跨重启保留。

夸克目录使用真实节点及父链，统一/逐条转存目标冻结到每条分享。QUARK_NODES 接入公共 DownloadService 的预览、目标、订单、输出占用、版本检查和 NONE 不归档规则；转存完成只接续目录刷新。失效目录不回退，未知文件大小保留 null。

Java 的领域、共享队列和 HTTP 用例覆盖本部分正常/失败/重启边界，并保留第一部分所有测试。实际 Java + Go 进程、SQLite、文件/侧车与收据已完成隔离联调；31 份实际响应/操作/结果捕获通过 schema 校验。复现方式及替身边界见 [跨进程说明](../../tests/integration/FEEDBACK-PART2.md)。

本机验证使用既有项目 Maven 3.9.16 缓存，临时 settings 仅选择已缓存的 aliyun-public 仓库；Java 21。沙盒访问旧缓存 JAR 的 toRealPath 被拒绝，因此该次离线 verify 经准确范围审批执行。未修改系统 Java、Maven 或代理。

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

上述第一部分行为由原提交保留；第二部分由新 Java 对话接续，并通过原业务与 HTTP 测试验证未破坏第一部分行为。

## 构建与测试

锁定 Spring Boot 3.5.16、sqlite-jdbc 3.53.4.0、Maven Wrapper 3.3.4、Maven 3.9.16、Surefire 3.5.6。使用 Java 21，在本模块运行：

```powershell
.\mvnw.cmd --batch-mode --no-transfer-progress -DfailIfNoTests=true verify
```

Carbon 验证复用旧后端已下载的锁定 Maven/依赖缓存，使用隔离测试目录；缓存中的依赖来自 `aliyun-public` 镜像，因此离线构建使用相同 mirror id 的临时 settings。该主机路径不进入产品配置。Windows Wrapper 修复了普通目录 Target=null 时的空数组索引；未更换 Wrapper 或依赖版本。

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

Java 已提供第二部分收藏夹、问题查询确认、夸克目录/分享及下载清单接口；Go 生产 Telegram/夸克会话客户端和真实转存 provider 尚未接入，默认 cmd/runner 仍只装配第一部分 Handler。隔离联调使用显式测试装配，不能作为生产可用声明。AUTO 后续归档任务可持久排队，但没有云端适配器/配置时不会成为 VERIFIED。真实账号、平台下载/转存、云端归档、Y7000P 部署、电源断电、界面视觉与七天运行均未验收。

Refs [#2](https://github.com/moonseries667/AllDownloads-v2/issues/2)、[#4](https://github.com/moonseries667/AllDownloads-v2/issues/4)、[#5](https://github.com/moonseries667/AllDownloads-v2/issues/5)。
