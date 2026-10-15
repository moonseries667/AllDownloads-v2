# Java 业务核心（M1 隔离替身阶段）

## 第二部分领域模块（接入前）

契约基线 `6233234`，完整组与目标边界补充 `9e7886f`。`dev.alldownloads.feedback2` 使用调用方的 SQLite Connection 和事务，不新建业务数据库、HTTP 服务或平台调度器。`TelegramLibrary` 保存原始扫描事实、完整组资格与待处理单元，最终扫描页通过 Enqueue 接入公共操作队列；`IssueRepository` 提供稳定持久问题；`QuarkCatalog` 核验节点父链、冻结转存目标和展开文件版本。普通构造和查询不扫描平台。

开工核验 B10/B11/B12 均只有契约，未有 Java 实现；现已新增上述领域能力和 `DomainBehaviorTest` 的 12 项 SQLite 用例。Maven verify 共 22 项通过，包括原核心恢复测试。公共 HTTP、订单快照和 Runner 接入等待第一部分已完成提交；此处不把领域测试称作集成通过。Carbon 上只用合成数据，真实 Telegram、夸克、Y7000P 与持续运行均未验证。

本机验证使用既有项目 Maven 3.9.16 缓存，临时 settings 仅选择已缓存的 aliyun-public 仓库；Java 21。沙盒访问旧缓存 JAR 的 toRealPath 被拒绝，因此该次离线 verify 经准确范围审批执行。未修改系统 Java、Maven 或代理。

Java 21 独占 SQLite 任务、租约、幂等请求、执行事实确认与后续操作；Go 不连接此数据库。本模块尚未接入公共 HTTP 和 Java-Go HTTP。当前模型只供内部合成 fixture 测试，不能作为 #2 的冻结协议，也不代表任何平台下载、上传或归档完成。

## 构建与测试

依赖锁定：Spring Boot 3.5.16、sqlite-jdbc 3.53.4.0、Maven Wrapper 3.3.4、Maven 3.9.16、Surefire 3.5.6。使用 Java 21，进入本模块运行：

```powershell
.\mvnw.cmd --batch-mode --no-transfer-progress -DfailIfNoTests=true verify
```

首次构建由官方 Wrapper 下载 Maven，之后复用缓存。`JAVA_HOME` 指向 Java 21；不修改系统 Java。构建产物位于 `target/`，Spring Boot jar 可启动回环 HTTP 容器，但当前无业务路由、无调度实例，不创建正式数据库或访问平台。不要将容器成功启动当作后端业务已可用。

测试使用 JUnit 5、临时目录与真正 SQLite 文件，验证 R01/R04/R06/R07/R10/R11/R12 对应的内部机制：请求去重、事务失败回滚、重复确认、旧租约隔离、暂停保存、取消竞争、目录版本固定、排他锁以及子 JVM 被强制终止后的恢复。Go 文件发布与收据事实由 Go 测试验证；此处合成 Evidence 是状态机测试输入，不能当作真实文件验收。

## 内部边界与恢复

`TaskStore` 是唯一 SQLite 写入者，使用 WAL、FULL 同步、3 秒 busy 超时、短事务及进程文件锁。任务保存不可变资源/路径/内容/存储版本，幂等键相同但内容变化会冲突；同资源或同目标不会生成第二任务。任务状态、已确认结果、后续步骤与 ack 在同一事务提交；数据库失败不会返回成功确认。

替身只有 `WRITE_FIXTURE` → `VERIFY_FIXTURE` 两个阶段。第二阶段代表需要再次核对的调度步骤，当前不是实际平台上传。任务只有最终核对报告符合预期路径、UTF-8 字节数和 SHA-256 才成功；首个文件报告本身不会结束整条链。任何文件证据都由执行边界提供，Java 不创建媒体文件。

每次领取递增 attempt/fencing token；报告校验 operation、task、runner、lease、attempt 和 token。过期操作进入 NEEDS_ACTION/RESULT_UNKNOWN，不自动重领。保留原租约用于明确 reconcile；只有执行方确认可安全恢复的无副作用失败才进入重试。默认 30 秒、2 分钟、10 分钟，最多三次自动重试，Retry-After 可延长等待。

取消/暂停先持久化意图；在途任务必须等执行确认。取消与文件发布竞争时保存成功操作的事实，阻止下一步调度；最终已成功任务不会被迟到取消覆盖。保存的暂停跨重启保留。重复 eventId 相同内容返回原持久确认，变化返回冲突；乱序心跳不回退进度或延长租约。

数据库 schema 为首个内部版本 1，不迁移旧版 AllDownload 数据；仅适用于可重建的隔离测试数据库。不要将此阶段数据库用于正式部署。后续公共模型与 schema 演进由契约和部署阶段审阅。

## 依赖与未完成验收

语义基线 `dc53d92`，参见 [HTTP 语义](../../contracts/HTTP-API.md) 与 [执行语义](../../contracts/RUNNER.md)。机器可读契约、内部认证、公共任务路由和跨进程 HTTP 字段仍由 [#2](https://github.com/moonseries667/AllDownloads-v2/issues/2) 冻结；本次没有修改 contracts。提供内部入口 `TaskStore.create/acquire/heartbeat/control/complete/reconcile`，供冻结后添加适配器。

[#5](https://github.com/moonseries667/AllDownloads-v2/issues/5) 承接三端联调。平台 I/O、真实上传/核对、Y7000P 部署、电源断电与七天持续运行均未验证；R02/R03 的真实媒体续传、R05/R08/R09 仍待平台实现。不得将各模块测试写为三端联调通过。
