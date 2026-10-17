# Go 执行程序：媒体、取消控制与存储核验

本模块在 Carbon 开发和隔离验证，正式运行目标仍为 Y7000P。Java 唯一决定选择、路径、任务、重试和归档；Go 取得平台事实并执行冻结计划，浏览器不调用 Go。当前共同契约已通过 PR #29 合入主线 `0b34a82`，保留第一部分 `d508c7a` 的兼容语义；协议版本和 payload schemaVersion 均为 1。

正式命令已装配单账号 Bilibili、Telegram `tdl 0.20.4`、夸克原生 HTTP、Drive 同源授权和受管 G 盘挂载，复用同一发布、收据、租约及取消循环。完整 Windows 包、同源前端及启停方式见 [Y7000P 发布说明](../../docs/Y7000P.md)。生产适配器的本地协议替身验证与 Y7000P 的真实账号、文件及界面验收分别记录。

## 正式启动与新会话配置

Bilibili 读取和下载使用同一个 `bilibili-main` 会话；首次由用户在账号页扫码，后续登录必须匹配已绑定身份，失效不匿名降级。Drive 按用户明确指定的既有 rclone 配置共用 `gdrive` 授权，原 DPAPI OAuth 作为另一显式模式保留；详细接入见 [Bilibili](internal/platforms/bilibili/README.md)、[Drive 挂载](internal/drive/MOUNT.md)。tdl 和 Drive 固定走用户指定的 WARP 40000 代理，故障不直连退化。Telegram/夸克首次设置通过 [session-setup](cmd/session-setup/README.md) 在 Y7000P 交互终端完成，再重启并主动检查账号。普通 GET 和启动不触发登录或收藏扫描。

复制 [runner.example.json](runner.example.json) 到不入库的 `runner.local.json`，按实际运行主机修改绝对路径。Java 的 `alldownloads.runner-token-file` 与 Go `tokenFile` 指向同一个私有内部认证文件；也可只配置 `tokenEnv`，两者不能同时配置。token、Cookie、账号会话都不放到参数、日志或 Git。

先构建并启动 Java，再运行 `go build -o bin/runner.exe ./cmd/runner`、`bin/runner.exe -config <runner.local.json>`；`-once` 只注册、恢复收据并最多执行一个操作。默认未配置账号的操作返回明确 AUTH_REQUIRED；缺 FFmpeg/会话/身份不伪造成功。Telegram 配置为空时启动不会检查 tdl；配置账号后只在本地核对工具版本，普通启动仍不访问收藏夹。

Telegram 通过 `session-setup --platform telegram` 建立新的 namespace/storage，用户亲自扫码，工具再以只读 Self 请求绑定 `peerId`，不列聊天或扫描收藏来推断身份。工具来源是 [tdl 0.20.4 官方发布](https://github.com/iyear/tdl/releases/tag/v0.20.4)，操作依据 [登录](https://docs.iyear.me/tdl/getting-started/quick-start/)、[结构化导出](https://docs.iyear.me/tdl/guide/tools/export-messages/) 与 [下载](https://docs.iyear.me/tdl/guide/download/)。Go 每次解析都会核对身份；不要运行缺少新 storage 的登录或 Desktop 导入命令。

tdl 会在进程初始化访问用户目录；Go 仅为自己的子进程将 USERPROFILE/HOME 固定到 `<stateDir>/telegram-work/tool-home`，避免隐式读取默认 `.tdl`。显式 storage 仍指向上述新会话目录。工具日志留在私有运行范围，错误只映射受控原因；限流返回等待秒数，取消终止该文件的受控进程树。每次下载只输入一个冻结 message，不启用 `--group` 扩大操作范围，下载前后核对附件身份，发布仍由统一 executor 完成。

夸克通过 `session-setup --platform quark` 在 Y7000P 隐藏输入新 Cookie，并只读核对、绑定真实 `uid` 后保存 `cookieFile` / `expectedUserId`。程序不读取旧应用或浏览器凭据。会话过期返回 AUTH_REQUIRED，更新同一账号的新会话后显式重试/核对；账号变化阻止旧意图继续。Native HTTP 请求形状参考 [作者实现](https://github.com/Cp0204/quark-auto-save/blob/main/quark_auto_save.py) 与 [OpenList Quark 类型](https://github.com/OpenListTeam/OpenList/blob/main/drivers/quark_uc/types.go)，保留原生 Go HTTP 适配。

转存前持久化账号、源快照、固定目标与节点基线；超时后按 task/remote ID 或新节点的强校验事实核对。RESULT_UNKNOWN 只提供 RECONCILE，核对操作不发 save；证明剩余从未发送后才恢复同一 operationId，PAUSE/CANCEL 意图继续生效。每小时追更及手动刷新由 Java 统一调度，新增只转存；选择下载另走冻结清单。下载校验稳定节点、版本、长度与平台 SHA1/MD5，再由 executor 记录本机 SHA-256。

`fixtureCatalog`、`quarkAccounts.*.baseUrl/accountUrl` 只在显式 `isolatedTestMode=true` 下启用，HTTP 测试端点限定回环 IP。没有生产替身 fallback。Driver 目录纯选择不写云端；上传可按持久预留 ID 自动补齐必要相对目录，响应丢失后核对同一 ID，重名不唯一仍明确失败。Drive token 的授权/更新由正式运行配置承担，过期不会被当作已上传。

第三部分消费共同契约 `baa03f5`（本分支等价提交 `92e4aac`）、第一部分 Go `963da9a` 和第二部分 Go `5192959`。以下第三部分说明覆盖旧段落对上传/目录能力的“未实现”描述；历史平台核验范围不变。

## 第三部分 B14—B17

| 项目 | 基线核验 | 当前执行入口与事实 |
| --- | --- | --- |
| B14 | 原 runner 只能取消完整 operation，没有持久控制意图 | Java 按 Bilibili 单一 unit、Telegram 单一附件加侧车、夸克单一 node 分配独立 operation；Go 沿用 heartbeat CANCEL，先持久写控制意图再停止网络、流和工具。Job Object 终止后检查 activeProcesses=0；无法确认时 RESULT_UNKNOWN。完成竞争保留真实文件，重启 CANCEL 不复活，PAUSE 的新租约可显式恢复。共享消费者与顶层范围由 Java 管理。 |
| B15 | 没有 Drive 上传执行器 | `drive.Client.Archive` / `StorageWorker` 仅接受 AUTO 冻结文件清单，逐项核对本地文件；预留且持久化远端 ID 后流式上传，再 GET 精确对象核对身份、父级、名称、大小和 SHA-256/MD5。HTTP 成功或子进程退出不等于 VERIFIED。应答丢失后按同一 ID 核对，不重下媒体、不删除本地成品。入队资格、整项目完整性与 NONE 排除仍由 Java 事务负责。 |
| B16 | 没有宿主目录选择 | `folder.Picker` 运行同一个 runner.exe 的内部 helper，COM STA 调用 IFileOpenDialog + PICKFOLDERS/FORCEFILESYSTEM。只读探测当前会话、可见 window station 和输入桌面；Session 0/锁定或不可访问桌面返回不可用，不切换会话、不安装服务。最多 120 秒，取消关闭受控进程树。选中后复用 Handler 的本机允许范围、rootId 和可写验证，选择不保存全局设置。 |
| B17 | 没有 Google Drive 目录适配 | `drive.Client.List/Validate` 使用官方 v3 GET、实际 root ID、父链、原始名称、canListChildren/canAddChildren 和分页。重复名称保留不同 ID；失效、移动/改名、权限、认证和 cursor 错误分别返回。纯选择不创建云端对象。当前仅 My Drive 范围，不声称支持 shared drives。 |

第三部分没有新增 runner、业务数据库或业务调度器。`runner.StorageWorker` 包装第一/二部分 Worker，新增 SELECT_LOCAL_DIRECTORY、LIST_DRIVE_FOLDERS、VALIDATE_DRIVE_TARGET、UPLOAD_ARTIFACT；目录与上传的 wire 字段逐项消费第三部分契约。`Recover` 对上传只读核对，不在启动重放时发送媒体。

### 运行配置与边界

原配置继续有效。原生选择自动按实际桌面能力注册 `facts.directoryPicker`；hostId 使用已配置 runnerId，不返回浏览器路径。公共入口是否来自同机回环浏览器由 Java 校验。选中目录仍须在 `allowedRoots` 内，不能因为对话框能看见某路径就绕过写入授权。

Drive 是可选配置；没有 `driveAccounts` 时不注册 Drive 操作。每项只保存 Go 运行时所用环境变量**名称**，不把 token 值写入配置、参数或 Git。示例：

```json
{
  "driveAccounts": {
    "drive-account": {"tokenEnv":"ALLDOWNLOADS_DRIVE_ACCESS_TOKEN","rootId":"root"}
  }
}
```

账号授权和 token 生命周期由后续已获准的运行配置负责，本模块不替用户 OAuth 登录或读取旧会话；缺失/过期返回 AUTH_REQUIRED。实际根或受控子目录可以作为 rootId，列表不越过它。预览用路径只展示，执行使用 folderId 及冻结 revision，后续全局设置不会重定向旧上传。

目录选择不创建云端目录；上传阶段会为冻结文件清单建立必要相对层级，先预留稳定 ID 并持久化再创建。重名不唯一返回 ARCHIVE_DIRECTORY_AMBIGUOUS，不回退根目录；只读 Verify 不重建被删除或替换的目录。

### 第三部分验证与交接

Carbon 已使用 Go 1.27.1、隔离缓存、Windows cgo 和 FFmpeg 9.0.2 执行真实临时文件/HTTP/受控进程测试：

* `internal/media/cancel_test.go`：HTTP 音轨关闭、合并不发布、先前成品保留；真实子进程及其子进程停止，无关兄弟进程继续写完成文件。
* `internal/platforms/binding/cancel_test.go`：独立租约取消夸克单文件；同一 Telegram 媒体组取消视频后音频/照片及各自侧车继续，重开收据不重复抓取。后者需要下方 FFmpeg 环境变量；本轮已设置并执行，不是跳过。
* `internal/runner/cancel_test.go`：控制意图持久化、取消后重开不执行、真实文件发布与取消竞争保留成功。
* `internal/drive/client_test.go`：重名/分页/权限变化/删除/认证、冻结清单、NONE/未验证/.work 拒绝、上传响应丢失、只读恢复、远端缺失及名称/父级/大小/校验不一致、MD5 核对；远端为本地 HTTP 替身。
* `internal/folder` 和 `runner/storage_test.go`：选择成功、用户取消、超时、忙碌、不可用及宿主目录校验；没有保存全局设置的副作用。

本目录复现：设置 `ALLDOWNLOADS_TEST_FFMPEG_DIR` 为现有固定 FFmpeg 9.0.2 bin，再运行 `go test -race -count=1 ./...`、`go vet ./...`、`go build ./...`。Java 负责人可直接组合 `StorageWorker{Next: &binding.Worker{...}}`，通过同一注册/租约/结果协议联调；没有向其他聊天发送指令。

**真实验收尚缺**：Windows 交互对话框的视觉/点击、Telegram/夸克/Drive 账号及业务媒体、Y7000P 部署与七天运行。跨 Java/正式 Go 进程的隔离协议验证见文末，不记为账号验收。

接口依据：[Windows Common Item Dialog](https://learn.microsoft.com/en-us/windows/win32/shell/common-file-dialog)、[Drive files.list](https://developers.google.com/workspace/drive/api/reference/rest/v3/files/list)、[Drive 文件元数据与校验值](https://developers.google.com/workspace/drive/api/reference/rest/v3/files)、[Drive 流式上传](https://developers.google.com/workspace/drive/api/guides/manage-uploads)。这些资料用于源码实现，本轮没有用真实账号调用上述云端 API。

## 核验与实现

开工基线 `bb9a1f2` 只有 `internal/executor` 的合成文本发布、Windows 排他锁、持久意图/收据和恢复测试，没有平台适配器、媒体执行、HTTP transport 或常驻 runner。

| 项目 | 基线结论 | 本轮入口与范围 |
| --- | --- | --- |
| B01 | 缺失 | `cmd/runner`、`internal/runner` 注册/领取/续租提供存活依据；工具版本经检查才注册。挂载返回 UNSUPPORTED、账号 UNKNOWN，不以目录存在声称挂载或归档成功。 |
| B02 执行侧 | 缺失 | `media.Bilibili.FetchSource` 分页读取收藏夹/合集，完整收集后返回；单视频/番剧/电影来源读取资料。失败不返回完整成员快照。排序、置顶和持久关系属于 Java。 |
| B03 | 缺失 | `media.Bilibili.Fetch` 解析单视频、多 P、以 season_id 独立的番剧/电影；按实际返回的 DASH 质量选轨。字幕同时检查语言、type、ai_type，仅明确人工字幕可选；未知/AI 写明排除原因。时长/大小可空，未为大小提前下载媒体。 |
| B04 | 缺失 | `runner.Handler` 保存私有 locator 快照，公开资料不含播放 URL；下载核对 revision、unit、track、outputs。空选择失败，新轨道不进入旧订单；按 MUX 合并音视频，单轨/字幕/弹幕可独立输出。 |
| B05 | 部分：仅单根文件执行 | allowedRoots + rootId 校验与实际可写探测；每个操作固定 rootPath，临时文件和发布均在该根。覆盖目标不复用默认根的成果。Go 不创建上传操作，NONE 的调度保证由 Java 完成。 |
| B06 事实侧 | 缺失 | 权限、限流、网络、资源不可用与未知错误分别报告；仅明确视频资料接口 -404 是 RESOURCE_DELETED。CDN 404、403、超时都不是删除证据。文件事实含实际字节数、SHA-256、媒体种类、可获得时长和 verified。 |
| B07 | 已有发布校验 | 继续执行 Java 路径；保留中文、Windows 非法字符/保留名、大小写冲突、reparse point 和不覆盖发布检查。不另造命名或自动搬迁。 |
| B08 | 仅文本替身 | 流式 HTTP、强 ETag/范围/长度校验、部分文件校验与恢复；FFmpeg/ffprobe 9.0.2 有界进程与 Job Object 进程树约束。发布前后验证，收据复用不重下。 |
| B09 | 缺失 | 回环 HTTP 注册/领取/续租/结果/reconcile；持久操作/结果日志、同 eventId 重放、租约失联停止、暂停/取消传播。重启从发布事实恢复完整/部分结果。Java 跨进程组合验收由 Java 对话执行。 |

## 构建与验证

Go 固定 1.27.1，目标 Windows amd64；没有第三方 Go 依赖，因此没有 go.sum。在本目录执行：

```powershell
go test -race -count=1 ./...
go vet ./...
go build ./...
```

Windows race 需要 cgo 和兼容 MinGW-w64；仅在当前终端设置 `CGO_ENABLED=1` 和准确 `CC`。完整媒体替身测试使用已安装的固定 FFmpeg 9.0.2：

```powershell
$env:ALLDOWNLOADS_TEST_FFMPEG_DIR = '<FFmpeg 9.0.2 的 bin 绝对路径>'
go test -race -count=1 ./...
```

未设置该变量时，仅 `TestRealFFmpegMergeAndSingleTracks` 跳过；其他元数据、HTTP、执行、恢复、契约及故障测试仍执行。Carbon 本轮设置了该变量，实际生成临时音视频并检查合并/单轨文件，没有业务媒体。

测试覆盖人工/AI/未知字幕、大整数 ID、多 P/分季、未知大小、电影语言单元及共享视频身份、精确选择、字幕/弹幕单独输出、ETag 续传/版本变化/损坏、取消、实际媒体流、独立目标、收据与发布恢复、确认丢失、暂停/取消/租约失联。`internal/runner/testdata` 的 wire 样例原样来自 `d508c7a:contracts/feedback1.examples.json`，往返测试防止丢字段或混淆 null。

## 启动与 Java 集成

```powershell
go build -o '<隔离输出目录>/runner.exe' ./cmd/runner
& '<隔离输出目录>/runner.exe' -config '<隔离配置绝对路径>'
# 注册、重放未确认结果，最多领取一个操作
& '<隔离输出目录>/runner.exe' -config '<隔离配置绝对路径>' -once
```

配置如下，路径仅为示例，部署时按实际主机填写。内部 token 由独立运行环境注入，配置只存变量名，不把 token 放在参数或源码中。

```json
{
  "runnerId": "runner-local",
  "controlUrl": "http://127.0.0.1:18080",
  "tokenEnv": "ALLDOWNLOADS_INTERNAL_TOKEN",
  "stateDir": "C:/AllDownloads-v2/runtime/runner",
  "allowedRoots": ["D:/Downloads"],
  "roots": {"download-root": "D:/Downloads"},
  "ffmpeg": "C:/AllDownloads-v2/tools/ffmpeg.exe",
  "ffprobe": "C:/AllDownloads-v2/tools/ffprobe.exe"
}
```

`controlUrl` 只允许明确回环 IP HTTP。`allowedRoots` 是运行配置授权的本机范围，必须已存在；用户目标只能在其内，不能使用 UNC、符号链接或 junction。`roots` 为 Java 已知默认 rootId 的固定映射；覆盖目录返回规范路径派生的稳定 rootId。配置变化不能将旧订单重定向。程序不读取浏览器/旧应用会话；有非空 accountRef 而未配置账号能力时返回 ACCOUNT_NOT_CONFIGURED，不伪称登录。

每个 stateDir 只有一个 runner，每个目标根也有 executor 排他锁。私有日志、平台 locator、临时文件、媒体和配置均为运行数据，不进入 Git。

Java 本地组合测试可增加 `fixtureCatalog`，指向临时 JSON 文件。该模式显式使用替身，所有可选 Source.URL 必须是回环 HTTP。格式是 `externalId -> media.Metadata`，例如字幕单项：

```json
{
  "BVFixture": {
    "ID": "content-fixture", "ExternalID": "BVFixture", "IdentifierType": "BVID",
    "Kind": "SINGLE", "Title": "隔离验证", "OriginalURL": "https://www.bilibili.com/video/BVFixture",
    "Units": [{
      "ID": "unit-fixture", "ExternalID": "cid-fixture", "Kind": "MAIN", "Index": 1,
      "Tracks": [{
        "ID": "subtitle-fixture", "Kind": "SUBTITLE", "Language": "zh", "LanguageName": "中文",
        "SubtitleType": "HUMAN", "Availability": "AVAILABLE",
        "Source": {"URL": "http://127.0.0.1:19090/subtitle.json", "Version": "fixture-v1"}
      }]
    }]
  }
}
```

先通过 FETCH_BIL_METADATA 或 USER_ADDED 来源同步让 Go 保存快照，Java 使用返回的 `content.revision` 和轨道 ID 创建预览/订单；不要直接把共享示例的 `fixture-v1` 当运行时 revision。测试 HTTP 返回 Bilibili 字幕格式，如 `{"body":[{"from":0,"to":1,"content":"测试"}]}`。视频/音频使用临时 FFmpeg 文件与 VIDEO/AUDIO 轨道；DANMAKU 返回 `<i>...</i>` XML，或平台实际返回的 raw DEFLATE 实体。压缩弹幕在下载缓存之后解压至独立工作文件，校验后发布 XML；保留原始输入和下载状态，既有缓存也能重试。有效的空 `<i>` XML 表示源站未提供弹幕条目。此 catalog 是 Go 私有测试入口，不是另起平台协议，也不自动填充真实目录。

## 实质限制与真实验收

原生请求结构参考 [yt-dlp Bilibili 提取器](https://github.com/yt-dlp/yt-dlp/blob/master/yt_dlp/extractor/bilibili.py)，字幕证据参考 [字幕 API 参数追溯](https://github.com/gcnyin/bilibili-subtitle-api)。接口变化、WBI、账号权限、付费/地区限制、收藏夹/合集大规模扫描和实际 XML 弹幕须在获准的目标与操作范围内于 Y7000P 分别实测，不以单个视频或替身通过代替。

电影各版本按平台真实单元返回，不伪造 P；依据实际 DASH audio 的 language/language_doc 等字段按语言保留最高可用音轨，形成 LANGUAGE 单元并保留共享视频身份，字段依据见 [BBDown Rust 平台解析器](https://github.com/Joey-Project/BBDown-rust/blob/master/crates/bbdown/src/client.rs)。未提供语言的音轨不猜标签。额外 `dubbing_info` 结构遇到时报告 ALTERNATE_AUDIO_METADATA_UNSUPPORTED。非 DASH/DRM/未知媒体结构不伪造可下载轨道。各平台会话及 G 盘组件已接入；真实账号、Explorer、云端结果与长期运行均独立验收，本地替身不能替代这些事实。

## 正式入口的隔离跨进程复现

先按 Java README 完成 Maven verify，构建正式 Runner 与仅测试使用的 CLI 协议子进程，再在仓库根执行：

```powershell
# backend/go 目录
go build -o bin/runner.exe ./cmd/runner
# tests/integration/tdl-protocol 目录
go build -o ../../../backend/java/target/tdl-protocol.exe .
# 仓库根目录；使用项目隔离 Python 环境安装 requirements.txt
python tests/integration/platform_clients_process.py --java '<Java21>/bin/java.exe' --runner '<仓库>/backend/go/bin/runner.exe' --tdl-protocol '<仓库>/backend/java/target/tdl-protocol.exe' --ffmpeg-dir '<FFmpeg9.0.2>/bin'
python tests/integration/validate_runner_lifecycle.py '<输出 runtime>/runner-http.json'
```

脚本启动真实 Java jar、生产 `cmd/runner`、本地夸克原生 HTTP 协议替身和受控 tdl CLI 协议子进程，使用临时 FFmpeg 音频/照片。代理仅在隔离测试中将 Go 获得的心跳周期缩为一秒，并用已领取证明验证实际 facts HTTP；正式 Java 的默认周期仍为十五秒。监控场景实际等待持久六十秒周期。

验收覆盖普通启动不扫 Telegram、首次资格过滤/混合附件侧车、增量/启用监控、真实流取消和兄弟照片继续、未知转存不盲重发/用户只读核对、明确选择才下载、失败重试、追更新增不扩大旧订单、NONE 不上传、提交确认丢失后重放及 Java/Go 重启不重复 I/O。日志、SQLite、收据和 HTTP 捕获留在 `backend/java/target/platform-clients-*`，不进入 Git。官方 tdl 0.20.4 二进制另做了本地 version/help 核对；协议子进程通过不代表实际 Telegram 账号/MTProto 已验收。

本地替身通过不等于 Java 跨进程组合、前端视觉、真实平台、Y7000P 部署或七天稳定性验收。后端集成归 Java 对话，读取本分支明确提交后在其工作树合入；不合并 main，不触碰旧目录/服务。
