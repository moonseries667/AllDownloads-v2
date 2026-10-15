# Git 与项目协作指南

适用对象：AllDownloads-v2 的前端、Java、Go 和协调对话。仓库为 [moonseries667/AllDownloads-v2](https://github.com/moonseries667/AllDownloads-v2)，`origin` 固定为 `git@github.com:moonseries667/AllDownloads-v2.git`。

采用一个仓库、一个集成主分支、短期任务分支、每个开发对话独立工作树。共同契约先明确，再分别实现，最后按用户行为联调。Carbon 负责开发构建；Y7000P 负责正式运行。推送、合并、发布、部署是不同状态。

## 1 依据与取舍

这些指南没有统一、可信的“评分榜”。本项目以官方规范和作者原始指南为依据，用社区关注度辅助选择；星数不代表方案适合所有项目。下表为 2026-10-15 查阅结果，具体分工和验收门槛是结合本项目制定的规则。

| 参考 | 本项目采用的部分 |
| --- | --- |
| [GitHub Flow，GitHub 官方](https://docs.github.com/en/get-started/using-github/github-flow) | 分支承载一个具体变化，通过 PR 展示修改、检查和讨论，再合入主分支 |
| [Git worktree，Git 官方](https://git-scm.com/docs/git-worktree) | 一个仓库支持多个工作目录，各自拥有 HEAD 和暂存区，适合多个对话同时开发 |
| [Short-Lived Feature Branches，Trunk Based Development](https://trunkbaseddevelopment.com/short-lived-feature-branches/) | 尽早集成小任务；避免前端和后端分别开发数周后才首次合并 |
| [Conventional Commits 1.0.0](https://www.conventionalcommits.org/en/v1.0.0/) | 用类型、范围和明确结果描述提交；破坏性变更需要显式说明 |
| [GitHub Projects 最佳实践](https://docs.github.com/en/issues/planning-and-tracking-with-projects/learning-about-projects/best-practices-for-projects) | 拆分可交付任务、明确依赖、关联 Issue 与 PR、保持单一进度来源 |
| [Git Flight Rules，作者仓库](https://github.com/k88hudson/git-flight-rules) | Git 故障按具体场景处理；查阅时约 42.6k stars。仅作为故障查找入口，其改写历史、强推和删除示例不构成本项目授权 |

一个产品需要协调发布前端、Java 和 Go，放在同一仓库可以用同一个提交确定三者版本。当前规模无需长期 `develop`、`frontend`、`backend` 分支，也无需拆成三个仓库或设置复杂发布分支体系。

## 2 唯一事实来源

| 信息 | 放在哪里 | 负责人 |
| --- | --- | --- |
| 用户期望、业务边界 | [PRODUCT.md](PRODUCT.md)、对应前后端需求 | 协调对话 |
| 公共接口、执行协议及兼容变化 | `contracts/**` | 协调对话；Java/Go 提案，消费者核对 |
| 任务目标、负责人、依赖、进度、交接 | GitHub Issue | 该任务负责人维护，协调对话排优先级 |
| 本次修改、验证结果、评审与合并记录 | GitHub PR | 实现对话提交，协调对话集成 |
| 业务是否达到要求 | [ACCEPTANCE.md](ACCEPTANCE.md) 的编号和对应验证证据 | 协调对话组织，各模块提供证据 |
| 发布与真实部署状态 | 对应发布 Issue / Release 说明 | 协调对话 |

GitHub Project 看板如启用，只展示同一批 Issue，不另抄一份任务清单。聊天负责讨论，影响其他端的决定要落到契约或 Issue。文档负责长期规则，不逐日重复记录任务状态。

还没有 Issue 编号的轻量文档修正可以直接开 PR；跨端功能、故障修复、真实验收必须有可追踪任务。没有远程 Issue 写入授权的对话先给出可提交的任务正文，由已获授权的协调对话发布；不得伪造 Issue 链接。

## 3 文件与决策归属

| 角色 | 单写范围 | 负责决定 |
| --- | --- | --- |
| 前端 | `frontend/**`，包括本目录锁文件与前端测试 | 页面交互、客户端实现、契约 mock |
| Java | `backend/java/**` | 公共业务服务、状态机、业务数据库和调度实现 |
| Go | `backend/go/**` | 平台适配、真实 I/O、文件处理、执行收据及工具管理 |
| 协调与集成 | `contracts/**`、`docs/**`、根配置、`.github/**`、`scripts/**`、`tests/integration/**` | 契约定稿、范围分配、合并顺序、共同检查与发布 |

Java 不执行平台 I/O；Go 不拥有 Java 业务数据库；浏览器只调用 Java。隔离工作树解决文件互相覆盖，目录归属解决两个对话同时修改同一设计的问题，二者都需要遵守。

同一角色同时有两个任务时，先把文件或模块分配清楚。发现需要改其他角色的范围，提交准确的变更提案，不顺手扩展本任务。依赖锁文件随所属模块一起维护，不建立由各端同时修改的根依赖文件。

`.github/CODEOWNERS` 登记真实 GitHub 账号。当前只有 `@moonseries667`，不会把对话名称写成不存在的 GitHub 团队；目录分工由上表落实。CODEOWNERS 是评审归属，不是本地文件写入锁或自动权限隔离。[官方说明](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/about-code-owners)

## 4 工作树、分支与当前工作的衔接

`main` 保存已集成的基线。初始化时允许协调者将已审阅文档作为首次提交推送到空仓库；之后功能通过任务分支和 PR 集成。`main` 在建设期可只有骨架，但不得宣称尚未完成的真实功能已经可用。

分支命名为 `<类型>/<角色>/<任务号>-<简述>`，例如 `feat/frontend/12-bil-selection`、`feat/java/13-download-orders`、`fix/go/25-path-validation`、`docs/coord/guide-update`。示例编号不代表仓库已存在这些 Issue。一个分支处理一个可以验收的变化；通常一到两个工作日内形成可审阅部分，较大任务拆小，不为追求天数强行提交破损实现。

建议目录：

```text
D:\Code\Other\AllDownloads-v2                      协调 / main
D:\Code\Other\AllDownloads-v2-worktrees\fe-12      前端任务分支
D:\Code\Other\AllDownloads-v2-worktrees\java-13    Java 任务分支
D:\Code\Other\AllDownloads-v2-worktrees\go-14      Go 任务分支
```

这些目录只是开发布局，不写入产品配置，也不代表已经创建了工作树。由协调者在核对路径、已有分支和权限后建立；每个对话以分配到的目录为根。主工作树和每个关联工作树共享 Git 对象与 refs，但不共享未提交文件。

```powershell
# 协调目录；先确认没有其他对话在进行 Git 操作。
Set-Location 'D:\Code\Other\AllDownloads-v2'
git status --short --branch
git worktree list
git remote -v
git fetch origin
# 使用已存在的 origin/main；目标目录和新分支均须未被占用。
git worktree add --no-track -b feat/frontend/12-bil-selection 'D:\Code\Other\AllDownloads-v2-worktrees\fe-12' origin/main
```

`--no-track` 避免新功能分支误把 `origin/main` 当自己的推送目标。工作树的创建、归档、共享远端配置由协调者串行操作；开发者只操作自己工作树的暂存区和任务分支。不要让两个对话检出同一分支，不强行绕过 Git 的占用检查。App 管理的工作树通过 App 的归档机制收尾；普通工作树由协调者核对已提交、已推送及忽略文件后按准确授权处理。

**2026-10-15 的衔接约定：** 前端已经在主目录开始构建，首次治理提交只纳入需求和协作文件，不夹带未交接的 `frontend/**`。这一轮可按现有“共享目录、文件单写、协调者统一 Git”方式完成；前端负责人交接准确文件清单与验证后再由协调者提交。期间其他对话不要在主目录切分支或 pull。下一项独立任务转到工作树。不要移动、清空或自动 stash 正在进行的前端内容。

## 5 一次任务从开工到推送

1. 读 `AGENTS.md`、本指南、模块需求和共同契约。核对主机、当前工作树、分支、远端和已有修改。
2. 明确本任务的用户结果、文件范围、验收编号、依赖任务与契约基线提交。确认前置契约已合入；被阻塞时继续不依赖它的模块内部工作。
3. 实现一个完整变化，运行受影响检查；检查差异后仅暂存自己的文件。
4. 本地提交，向同一仓库的自己任务分支推送，建立指向 `main` 的 PR。未完成时用 Draft；PR 正文使用仓库模板。
5. 协调者审阅接口影响和验证证据，消费者核对跨端变化。解决问题后完成集成；模块完成与真实验收分别记录。

以下命令在**独立前端工作树**运行，目录和分支按实际任务替换：

```powershell
Set-Location 'D:\Code\Other\AllDownloads-v2-worktrees\fe-12'
git status --short --branch
git branch --show-current
git remote get-url origin
git diff -- frontend
# 执行模块 README 声明的检查，并检查新文件内容。
git add -- frontend/src frontend/tests frontend/package.json frontend/package-lock.json
# 仅列出实际存在且本任务修改的路径；上行是示例，不是全量暂存指令。
git diff --cached --name-status
git diff --cached --check
git diff --cached
git commit -m 'feat(frontend): add explicit Bilibili download selection'
git push --set-upstream origin feat/frontend/12-bil-selection
```

Java、Go 按自己的目录使用相同流程。推送到用户明确指定的本仓库和任务分支属于项目协作范围；不得扩展到其他仓库、账号或部署目标。共享主目录仍只由协调者执行 Git 写操作。不要使用 `git add .`、`git add -A` 或 `git commit -a` 收走其他对话的修改。

## 6 提交、更新与合并

提交格式：`type(scope): 具体结果`。常用类型为 `feat`、`fix`、`docs`、`test`、`refactor`、`build`、`ci`、`chore`；范围使用 `frontend`、`java`、`go`、`contracts`、`repo`。正文可用中文。一次提交围绕一个可说明的行为，不把无关格式化和功能混在一起。破坏性契约变化使用 `!` 并在正文说明影响和接入顺序。[规范](https://www.conventionalcommits.org/en/v1.0.0/)

在自己的分支有新主线需要集成时，先完成当前可验证提交并确认工作树干净，再执行：

```powershell
git fetch origin
git merge --no-edit origin/main
# 如有冲突，由相关文件所有者核对语义，解决后重新验证和提交。
git push
```

这里用 merge 保留已经共享的分支历史，不要求 rebase 后强推。若远端同一任务分支出现意外新提交，先查清写入者和差异再合并，不能以强推覆盖。协调主工作树仅在没有未交接内容时用 `git pull --ff-only` 更新；不能快进时先检查分叉原因。

PR 默认采用 **Squash and merge**，把一个完整任务作为主线一个提交；这是集成策略，不要求重写开发者已推送分支。PR 标题遵循提交格式。合并后从新的 `origin/main` 开新任务分支，避免在已 squash 的旧分支继续开发。远端分支和工作树的删除仍按用户授权执行，不自动清理。[合并方式及其限制](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/about-merge-methods-on-github)

不为当前规模配置自动合并队列。协调者一次合入一个准备好的 PR；有契约依赖时先契约、再提供方、再消费者。合并后的组合检查失败要优先修复或 revert 对应提交，不能让其他模块长期适配一个错误主线。

## 7 契约先行，前后端如何接上

`contracts/HTTP-API.md` 和 `contracts/RUNNER.md` 已定义语义。机器可读 OpenAPI、操作 schema 和共享示例尚需在 M1 阶段建立；在建立前，不得把各端临时类型当成已冻结协议。

每次跨端变化按以下顺序：

1. 发起者说明用户场景、输入输出、状态迁移、错误、幂等及兼容影响；标明哪些端受影响。
2. 协调者统一修改语义文档、机器 schema 和示例。前端核对可展示性，Java 核对状态与事务，Go 核对真实执行可行性。
3. 契约 PR 合入后，记录提交号。实现任务从包含该提交的主线开始；模拟数据遵守同一套示例。
4. 提供方先实现，再让消费者切换；仅新增兼容字段也检查未知字段、可空性、默认值和枚举处理。删除字段、改变含义和错误语义必须协调所有消费者，不能偷偷沿用相同版本号。
5. 协调者联调并逐项核对实际响应。代码、生成类型、示例和说明不一致时修正源契约与实现，不在前端加猜测字段兼容。

将来 OpenAPI 负责 HTTP 结构的机器校验，语义文档负责业务约束；两者需在同一契约 PR 内保持一致。生成代码保存在各自模块，更新生成代码时带上源契约提交和生成命令，不手工改生成结果掩盖错误。

例如“B 站选择视频下载”至少拆为契约、Java 订单持久化、Go 执行、前端选择与提交、跨端验收。前端可先用固定契约 mock；Go 可用临时文件；只有真正连通后才能关闭父级业务任务。夸克“转存追更”和“选择下载”分属不同流程，不能为了联调方便合并成自动下载。

## 8 任务与看板

先用 GitHub Issues 和 PR 即可；任务量增加再增加一个仓库看板，名称建议 `AllDownloads-v2`。不要求额外项目管理平台，也不为单人多对话建立复杂 Scrum 仪式。

每个任务至少填写：用户可观察结果、归属角色/对话、准确文件范围、契约基线、前置 Issue、验收编号、验证环境。一个任务只指定一个实际负责对话；同一 GitHub 账号下在正文区分角色，GitHub Assignee 仍是实际账号。

| 状态 | 进入条件 | 下一步 |
| --- | --- | --- |
| Backlog | 有需求，尚未细化 | 明确范围和验收 |
| Ready | 验收、文件归属及前置依赖清楚 | 分配任务分支与工作树 |
| In Progress | 已有明确负责对话开始实现 | 提供可审阅提交和 PR |
| Review | 实现完成，本地必要检查通过 | 协调者评审、组合检查与合并 |
| Verify | 代码已合并，任务承诺的联调/真实验收尚未结束 | 完成所需证据或明确失败任务 |
| Done | 达到该 Issue 约定的验收层级 | 关闭 Issue，保留 PR 和证据链接 |

阻塞作为附加标记，并写明“被哪个任务阻塞、缺什么、由谁解开”；不丢掉原状态。每个实现对话通常只推进一个主要任务，协调者优先处理契约和集成阻塞。不要用“完成 80%”代替可验收结果。

若使用 Project，字段只保留 Status、Area（frontend/java/go/contracts/integration）、Priority（P0/P1/P2）、Milestone；Blocked 和依赖可用标签或原生关系表达，避免同时维护重复字段。P0 为数据正确性、无法构建或阻断协作的问题；P1 为当前里程碑必要功能；P2 为不阻断目标的改进。看板尚未创建时在 Issue 正文写相同信息即可。

模块任务可以在约定的模块验证完成后关闭；父级用户功能任务必须等跨端验收完成。需要等待 Y7000P 的任务保持 Verify；不要对这种任务使用会在合并时自动关闭的 `Closes #...`，改用 `Refs #...`。代码合并不是全部任务的统一完成条件。

## 9 首轮里程碑与依赖

本表是工作划分，不是已完成进度或已创建的 GitHub Milestone。协调者按当前实际实现把工作录入 Issue，避免重复派发。

| 阶段 | 交付结果 | 主责与依赖 |
| --- | --- | --- |
| M0 协作基线 | 文档、目录归属、远端与模板可用，现有进行中工作完成交接 | 协调；当前指南属于这一阶段 |
| M1 最小贯通 | 固定契约；页面提交一个替身操作，Java 持久化，Go 领取并报告；重启可恢复 | 三端共同；依赖 M0；先合同后各端骨架和联调 |
| M2 B 站 | 信息浏览、用户选择下载、元数据与本地成果分别成立 | 前端/Java/Go；依赖 M1；对应 B01–B06、D01–D02 |
| M3 Telegram | 手动增量、监控开关、链接任务、消息展示与日期目录符合原行为 | 三端；依赖 M1；对应 T01–T07 |
| M4 夸克 | 转存追更独立；网盘选择下载；分享后缀目录稳定 | 三端；依赖 M1；对应 Q01–Q06、D01–D02 |
| M5 统一存档 | 本地成果上传、远端核对、失败恢复和清理预览一致 | Java/Go 主责，前端展示；使用 M2–M4 统一成果；对应 A01–A03、R01–R12 |
| M6 Y7000P 交付 | 发布包脱离 Carbon 运行，真实业务验收、故障恢复及七天观察完成 | 协调组织；依赖相关功能和授权；对照完整 ACCEPTANCE |

M2、M3、M4 可以按实际人员与任务归属并行，统一任务链路和存档基础也可提前用替身验证。不要等三个平台全部写完才做第一次联调；每个平台先贯通一个最小场景，再补边界和故障路径。七天观察只在正式环境实际运行后计时。

## 10 检查、PR 审阅与完成定义

每个 PR 写清问题和结果、关联任务、契约影响、执行过的命令和结果、未验证部分。界面变化提供可审阅截图或操作说明；截图中不含私人收藏、会话和账号资料。不要粘贴大量成功日志替代结论。

| 修改范围 | 必要验证 |
| --- | --- |
| 纯文档 / 工作规则 | 一次内容与差异检查、相对链接和格式核对 |
| 前端 | 类型检查、受影响的交互测试与构建；关键选择/错误状态检查 |
| Java | 模块构建、受影响业务/持久化测试，接口契约校验；恢复变化需相应故障用例 |
| Go | 模块构建、受影响单元/I/O 替身测试；路径、取消、收据或并发变化需相应场景 |
| 公共契约 | schema/示例校验、提供方与消费者一致性、受影响端构建或契约测试 |
| 跨端流程 | 一次代表性组合链路；不能把三端各自 mock 通过写成联调通过 |
| 正式平台 / 发布 | Y7000P 上已授权真实 I/O、恢复和部署验证；持续运行单独记录 |

各模块首个骨架 PR 必须提供自己的 README、锁定工具版本、标准构建/测试入口和最小有效测试。协调者随骨架就绪接入 GitHub Actions；不要注册不存在的必需检查、使用 `continue-on-error` 隐藏失败，或用 `echo success` 表示测试通过。

当前治理基线**尚未配置自动 CI**。后续 CI 使用隔离数据，不访问正式账号，不携带 Y7000P 会话，不自动上传业务媒体或安装生产服务。Windows 路径行为必须有 Windows 验证；只通过 Linux CI 不足以证明部署适配。共同检查入口成熟后可增加一个始终运行的汇总检查，必需检查仅选真实存在且稳定产出结果的名称。

PR 合并前，协调者确认范围归属、需求/契约一致、必要检查通过、未验证事项有明确承接任务。只审阅最终提交；评审后产生影响行为的新修改要重跑对应检查。功能任务 Done 的条件是其验收结果已出现，并留下可定位的版本与环境证据。

## 11 GitHub 设置建议及现状边界

以下是仓库维护者可配置的目标，**本指南和模板不会自动设置 GitHub 分支保护、看板、Issue 或 CI**。

| 项目 | 建议 |
| --- | --- |
| 默认分支 | `main` |
| 日常合并 | PR，默认 Squash and merge，由协调对话完成集成 |
| main 规则 | 要求 PR、解决评审讨论；禁止强推和删除；实际检查建立后再设为必需 |
| 批准数量 | 当前同一个账号承载多对话，不要求另一 GitHub 账号批准；协调者记录实质审阅结论 |
| CODEOWNERS | 当前指向仓库所有者；有真实协作者后再按实际账号细分 |
| 自动部署 | 关闭；发布不自动操作 Y7000P |

PR 作者不能批准自己的 PR，因此当前不设置“至少一位其他账号批准”或强制 Code Owner 批准，避免同一账号工作流被锁住。未来有第二位实际维护者时再调整。分支保护功能还取决于仓库可见性和 GitHub 套餐；不能用文档声明替代实际配置状态。[批准限制](https://docs.github.com/en/pull-requests/how-tos/review-pull-requests/approving-a-pull-request-with-required-reviews)、[分支保护](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches)

## 12 发布与 Y7000P 交接

三个部分作为一个产品发布，候选版本使用如 `v0.1.0-rc.1`，验收通过后正式版本如 `v0.1.0`；这些只是命名示例，不代表现在可发布。发布必须对应一个确定的 main 提交，不随意组合三个分支的“最新”产物。

发布说明记录：提交号、前端/Java/Go 与外部工具版本、契约版本、通过的验收编号、未验证项、配置变化、数据库变更与回退条件。新版本自身的数据库演进需要受控迁移；“不迁移旧版 AllDownload 数据”不表示今后的 v2 可以随意丢数据库。

仅将构建产物和无凭据配置模板交给 Y7000P；部署目录、运行账户、运行环境及服务变更按部署任务明确。Git push、PR 合并或创建 tag 均不等于部署授权。Y7000P 使用已验收版本，不在生产目录直接开发或追踪任意功能分支。发现发布问题优先回退应用版本；若数据库不向后兼容，必须按该次发布的恢复方案处理，不能只替换旧程序。

旧版 AllDownload 的已禁用任务保持停用。正式数据库、登录态、下载内容、缓存和真实运行日志留在 Y7000P 的指定数据目录，不进入本仓库。Git 管理源码和规则，不能代替业务数据的恢复安排。

## 13 常见协作故障

| 情况 | 处理方式 |
| --- | --- |
| 工作区有其他对话未提交内容 | 保留；只处理本任务文件；不要自动 stash、覆盖或清空 |
| `index.lock` / 同时操作共享 Git | 先确认是否有命令仍在运行，由协调者处理；不直接删锁文件 |
| 契约与 mock 不一致 | 回到源契约确认并修正，不继续各自猜字段 |
| 推送被拒绝 / 远端前进 | fetch 后核对分支与新提交，解决差异再普通推送，不强推 |
| 已合入代码有问题 | 修复或新建 revert 提交，保留已共享历史 |
| SSH 22 端口不可达 | 可用 GitHub 官方 `ssh.github.com:443`，保留认证与主机密钥校验；不改系统代理或关闭校验 |
| 检查未运行 / 没有测试 | 如实记录；代码行为变化补必要验证后再宣称通过 |

Carbon 本次已验证 GitHub 官方 443 SSH 可读取目标仓库。若本地仓库设置了 `core.sshCommand`，它只保存在本机 `.git/config`，不会随源代码推送；其他机器按自身连接条件处理。不能读取或复制私钥来“同步环境”。[GitHub 官方 443 SSH 说明](https://docs.github.com/en/authentication/troubleshooting-ssh/using-ssh-over-the-https-port)

## 14 每次交接只需要这些信息

```text
任务 / Issue：
角色、工作树、分支：
本次用户可见结果：
契约基线与变化：
提交号、PR、推送状态：
已执行验证（命令、环境、结果）：
未完成验收、阻塞及依赖：
剩余未提交内容及归属：
下一位接手者需要做什么：
```

引用已有文档和 PR，不重复复制整份需求。后续开工提示见 [PARALLEL.md](PARALLEL.md)。
