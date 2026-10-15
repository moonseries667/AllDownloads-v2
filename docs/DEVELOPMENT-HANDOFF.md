# 前后端构建对话协作说明

把下方对应代码块完整发给负责实现的对话。仓库为 [AllDownloads-v2](https://github.com/moonseries667/AllDownloads-v2)，[看板](https://github.com/users/moonseries667/projects/1) 复用同一批 Issue。主线集成由协调对话处理，两个实现对话各自提交、推送和维护 PR。

## 当前工作分配

| 对话 | Carbon 工作树 | 分支 | 任务 |
| --- | --- | --- | --- |
| 前端 | `D:\Code\Other\AllDownloads-v2-worktrees\frontend-3` | `feat/frontend/3-workbench` | [#3](https://github.com/moonseries667/AllDownloads-v2/issues/3) |
| 后端（Java + Go） | `D:\Code\Other\AllDownloads-v2-worktrees\backend-4` | `feat/backend/4-core-chain` | [#4](https://github.com/moonseries667/AllDownloads-v2/issues/4) |
| 协调 | 独立协调工作树 | 按契约或集成任务另建分支 | [契约 #2](https://github.com/moonseries667/AllDownloads-v2/issues/2)、[联调 #5](https://github.com/moonseries667/AllDownloads-v2/issues/5) |

路径是本轮分配，不写入产品运行配置。工作树须由协调者从合入协作基线后的 `origin/main` 创建；不存在时先报告，不在主目录自行切分支代替。各对话首次开工记录 `git rev-parse HEAD` 与 `git rev-parse origin/main`。Git 工作树隔离文件和暂存区，不自动改变聊天的沙盒写入范围；工作区外写入仍走工具的准确范围审批。

## 发给前端对话

```text
继续 AllDownloads-v2 前端构建，承接 Issue #3：
https://github.com/moonseries667/AllDownloads-v2/issues/3

工作树：D:\Code\Other\AllDownloads-v2-worktrees\frontend-3
分支：feat/frontend/3-workbench
远端：git@github.com:moonseries667/AllDownloads-v2.git
只编辑 frontend/**。

先核对实际主机、Git 根目录、分支、远端和 status；阅读该工作树的 AGENTS.md、README.md、docs/GIT-PROJECT-GUIDE.md、docs/CI.md、docs/PRODUCT.md、docs/FRONTEND.md、contracts/HTTP-API.md、docs/ACCEPTANCE.md 和本文件。Carbon 仅开发构建，正式运行在 Y7000P。

如你之前在 D:\Code\Other\AllDownloads-v2\frontend 开发：先停止旧目录的写入，核对属于你的源码、锁文件、测试和资产，再复制到上述独立工作树，保留旧目录原件。不要复制 node_modules、dist、运行数据库、账号会话或临时产物；不要在旧主目录切分支、pull、stash 或提交。已有内容先核对和接续，不重建覆盖。

先完成桌面工作台、契约 mock、加载/空/错误/重试状态和有意义的交互测试。浏览器只调用 Java。B 站信息、用户主动下载、存档分离；夸克转存追更和网盘选择下载分离；Tel 保持需求中的既有行为。接口缺项在契约 Issue #2 提出具体字段与语义，由协调者落入 contracts；可继续不依赖它的界面内部工作，冻结前不要宣称已经接通真实后端。

按阶段审阅并提交完整的可验证变化。frontend 至少提供 package.json、package-lock.json、非 watch 的 test、build 入口和 README；运行 npm ci、npm test、npm run build，并做必要桌面交互检查。不创建 Python/Node 业务后台，不扩展移动端目标。

本条指令授权你向上述仓库推送本任务分支、创建/更新对应 Issue 和 PR。只 git add 本任务准确路径，审阅暂存差异，以 feat(frontend): / fix(frontend): 等格式 commit，再 git push --set-upstream origin feat/frontend/3-workbench。建立指向 main 的 PR；未完成用 Draft，正文 Refs #3，按模板写验证结果和未验证部分。等待 CI Gate 通过；失败修复实际问题，不能关闭 CI 或改公共规则。

维护 Issue #3 和看板进度；工作开始为 In Progress，实现及检查就绪为 Review。依赖未满足时写明 #2 的具体阻塞，不编造完成。不得自行合并 main、强推、删除分支、部署 Y7000P、操作真实账号或上传业务媒体。最终交接工作树、分支、commit、PR、CI 链接、验证范围和剩余事项。
```

## 发给后端对话

```text
构建 AllDownloads-v2 后端，承接 Issue #4：
https://github.com/moonseries667/AllDownloads-v2/issues/4

工作树：D:\Code\Other\AllDownloads-v2-worktrees\backend-4
分支：feat/backend/4-core-chain
远端：git@github.com:moonseries667/AllDownloads-v2.git
只编辑 backend/java/**、backend/go/**，你统一负责两个目录的协作。

先核对实际主机、Git 根目录、分支、远端和 status；阅读该工作树的 AGENTS.md、README.md、docs/GIT-PROJECT-GUIDE.md、docs/CI.md、docs/PRODUCT.md、docs/BACKEND.md、contracts/HTTP-API.md、contracts/RUNNER.md、docs/ACCEPTANCE.md 和本文件。Carbon 仅开发构建，正式运行在 Y7000P。不迁移或读取旧业务数据和账号会话。

Java 21 拥有公共 API、业务状态、业务数据库和调度；Go 拥有平台 I/O、文件处理、受控外部工具及执行收据。Go 不直接读写 Java 业务数据库，Java 不直接执行平台下载。不要建立 Python/PHP/Node 业务后台或三套平台调度器。

先围绕一个本地替身操作实现最小闭环：创建持久任务、Go 领取与租约、执行、持久收据、结果确认；验证重复提交/报告、取消、超时、进程重启及失败恢复。接口缺项在契约 Issue #2 提出模型、语义、错误与兼容影响，由协调者冻结共同契约；可先实现不依赖未定字段的内部模块。不要自行改 contracts、前端、根配置、scripts 或 .github。用户主动下载和平台同步必须保持业务边界。

Java 提交 Maven Wrapper（包括 mvnw.cmd 与 .mvn/wrapper/maven-wrapper.properties）、pom.xml、README 和有效测试；执行 .\mvnw.cmd --batch-mode --no-transfer-progress -DfailIfNoTests=true verify。Go 提交 go.mod、必要的 go.sum、README 和有效测试；执行 go test -race -count=1 ./...、go vet ./...、go build ./...。命令在各模块目录执行；真实 I/O 未验证必须明确标注。

本条指令授权你向上述仓库推送本任务分支、创建/更新对应 Issue 和 PR。按可验证阶段检查差异，只 git add 自己的准确路径，以 feat(java): / feat(go): / fix(...) 等格式 commit，再 git push --set-upstream origin feat/backend/4-core-chain。建立指向 main 的 PR；未完成用 Draft，正文 Refs #4，按模板写命令结果、契约基线及未验证内容。等待 CI Gate 通过；失败修复实际问题，不关闭 CI 或绕过保护。

维护 Issue #4 和看板进度；工作开始为 In Progress，实现及检查就绪为 Review。跨端联调由 #5 承接，不能把各自 mock 通过写成真实联调通过。不得自行合并 main、强推、删除分支、部署 Y7000P、操作真实账号或上传业务媒体。最终交接工作树、分支、commit、PR、CI 链接、验证范围和剩余事项。
```

## 两个对话共同执行

需要同步主线时，先在自己的工作树完成当前可验证提交并确认干净，再 `git fetch origin`、`git merge --no-edit origin/main`，处理自己范围内的冲突后验证并普通 push。已共享分支不要求 rebase，不强推。main 的 PR 必须满足最新主线、`CI Gate` 和讨论解决要求。

任务状态使用 Backlog → Ready → In Progress → Review → Verify → Done；“Verify”表示代码合入但仍欠该 Issue 承诺的验收。模块任务与真实业务验收任务分别关闭；未完成真实验收的父任务用 `Refs #编号`，不用自动关闭的 `Closes`。没有看板写入能力时在 Issue 正文维护事实并交协调者同步，不假称状态已更新。

协作基础设施本身不冻结业务接口。#2 仍需完成机器可读 OpenAPI、Java-Go 操作 schema 和共同样例；两个构建对话可以先推进内部骨架，涉及共享字段的最终接入必须跟随合入的契约。
