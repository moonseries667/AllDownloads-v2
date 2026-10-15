# 持续集成与合并门槛

工作流：[CI](https://github.com/moonseries667/AllDownloads-v2/actions/workflows/ci.yml)。配置在 `.github/workflows/ci.yml`，校验脚本在 `scripts/ci/`。这些 Node 脚本仅用于开发检查，不是产品业务后台。

所有分支 push、指向 main 的 PR 以及手动触发都会运行。使用 GitHub 托管 Windows runner、只读仓库权限，无生产会话，无部署步骤。同一分支的新运行取代旧运行；已取消的运行不能提供通过证明。

## 检查内容

| 检查 | 行为 |
| --- | --- |
| Repository checks | 执行校验脚本的回归测试，检查 Git 跟踪文本的 UTF-8、Markdown 相对链接和围栏，发现模块并校验必要构建入口 |
| Frontend build and test | Node 24；`npm ci`、`npm test`、`npm run build` |
| Java build and test | Temurin Java 21；`./mvnw.cmd --batch-mode --no-transfer-progress -DfailIfNoTests=true verify` |
| Go build and test | 按 `backend/go/go.mod` 设置 Go；Windows cgo 工具链预检；`go test -race -count=1 ./...`、`go vet ./...`、`go build ./...` |
| CI Gate | 汇总以上结果；发现失败、缺失、取消或已有模块检查被跳过时退出失败 |

`CI Gate` 是 main 的唯一必需状态检查名，不能随意更名。模块检查名称保持独立，Gate 负责检查它们是否按实际模块存在情况运行。仅缺失模块允许 skipped，摘要会明确“未构建、未测试”。仓库当前文档基线不包含其他对话未提交的前端源码；因此初次绿色 CI 只证明仓库校验与 Gate 工作正常。

Git 跟踪清单中出现模块描述符或源代码，即启用对应模块检查。已有实现却缺清单会直接失败。前端至少提交 `package.json`、`package-lock.json`、有效 `test` 和 `build` scripts；Java 提交 `pom.xml`、`mvnw.cmd`、`.mvn/wrapper/maven-wrapper.properties`；Go 提交 `go.mod`，有依赖时提交 `go.sum`。只有 README 或 `.gitkeep` 的目录仍属于未实现模块。

每个模块首个实现 PR 要包含可运行的有意义测试及模块 README。`npm test` 应是非 watch 模式；Java 必须实际运行测试；Go 的并发/文件行为以 race 和相关用例验证。不用空测试、跳过所有测试或虚假成功命令满足 CI。

## 本地复现

在自己的工作树根目录使用 Node 24：

```powershell
node --test scripts/ci/repository.test.mjs scripts/ci/gate.test.mjs
node scripts/ci/repository.mjs
```

脚本仅扫描 `git ls-files`，因此新文件需要在检查差异后按本任务范围暂存，才能与 CI 的提交内容一致。模块命令在各自目录执行；不需要安装项目根 Node 依赖。

## 失败时如何处理

先查看失败的具体 job，再修复实际问题；Gate 失败通常来自上游失败或本应执行的模块检查缺失。契约或 CI 共同配置的问题交协调者，不通过关闭检查、增设 bypass 或强推绕过。main 前进后，在自己的干净任务分支 fetch 并 merge `origin/main`，重新等待检查。

Actions 使用官方 actions，引用已核对的提交 SHA；升级时同时核对对应官方版本、输入参数及 runner 要求。Go race 依赖 Windows MinGW-w64，工具链检查失败必须修复环境适配，不能把 race 移除当作通过。当前没有自动上传构建产物、自动发布或访问 Y7000P 的动作。

CI 不代替页面人工检查、三端联调、真实平台 I/O 和七天运行验收。任务是否可以关闭，以其承诺的验收层级为准。
