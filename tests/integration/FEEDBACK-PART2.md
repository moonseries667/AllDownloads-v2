# 第二部分隔离跨进程验证

`feedback2_process.py` 启动实际 Spring Boot Java 进程、实际 SQLite 业务库和实际 Go Runner 进程。Go 测试装配复用 `binding.Worker`、共享租约循环、持久收据、文件发布及 FFprobe；只有 Saved Messages/云盘提供方来自本地 fixture。转存尚无生产 provider，测试装配仅模拟转存结果，并调用实际目标核验与树遍历；不能将此项写成真实转存已实现。

测试创建短 WAV、PNG 和普通文件，检查筛选范围、完整媒体组、照片及逐附件侧车、部分失败后补齐、确认响应丢失后的同事件重放、重复增量零新增读取、夸克根/目录目标、冻结清单、真实字节数、本批 NONE 策略和 Java 重启。测试私有 token 只在自身临时进程环境传递，不输出、不提交；不读取正式账号或会话。

先按模块 README 构建 Java，准备锁定的 Go 1.27.1、Java 21、FFmpeg 9.0.2。仓库根执行（路径参数取当前主机实际工具）：

```powershell
go -C tests/integration/feedback2-runner build -o ../../../backend/java/target/feedback2-runner.exe .
python tests/integration/feedback2_process.py --java "$env:JAVA_HOME\bin\java.exe" --runner "$PWD\backend\java\target\feedback2-runner.exe" --ffmpeg-dir "$env:ALLDOWNLOADS_TEST_FFMPEG_DIR"
```

使用项目隔离 Python 环境安装 `tests/integration/requirements.txt` 后检查共同样例与实际响应：

```powershell
python tests/integration/validate_contracts.py backend/java/target/contract-captures.json backend/java/target/runner-capture.json
python tests/integration/validate_feedback2_contracts.py backend/java/target/feedback2-contract-captures.json backend/java/target/feedback2-process-captures.json
```

结果捕获、数据库、生成媒体、私有测试 token 和日志全部在已忽略的 `backend/java/target/`。脚本只结束自己启动的进程，保留本次测试目录供定位，不清理其他会话或原始数据。31 份捕获（4 份公共 HTTP 用例、27 份跨进程操作/结果/公开对象）已在 Carbon 通过 schema 校验；JSON Schema 解析只使用本地 Registry，不访问外部引用。

真实 Telegram/夸克账号绑定、实际平台下载/转存/权限变化、云端归档、Y7000P 部署、界面视觉与七天运行仍未验收。默认 `backend/go/cmd/runner` 尚未装配 Telegram/夸克生产会话客户端，不能仅凭本测试宣称正式启动命令已有这些能力。
