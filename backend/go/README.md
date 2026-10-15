# Go 执行程序：第一部分媒体与领取链路

本模块在 Carbon 开发和隔离验证，正式运行目标仍为 Y7000P。Java 唯一决定选择、路径、任务、重试和归档；Go 取得平台事实并执行冻结计划，浏览器不调用 Go。共同契约消费 Java 提交 `d508c7a`，协议版本和 payload schemaVersion 均为 1；本分支只修改 `backend/go/**`。

第二部分误派后已撤回；仅阅读说明，没有 Telegram/夸克第二部分源码、契约或测试改动。

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

先通过 FETCH_BIL_METADATA 或 USER_ADDED 来源同步让 Go 保存快照，Java 使用返回的 `content.revision` 和轨道 ID 创建预览/订单；不要直接把共享示例的 `fixture-v1` 当运行时 revision。测试 HTTP 返回 Bilibili 字幕格式，如 `{"body":[{"from":0,"to":1,"content":"测试"}]}`。视频/音频使用临时 FFmpeg 文件与 VIDEO/AUDIO 轨道；DANMAKU 返回 `<i>...</i>` XML。此 catalog 是 Go 私有测试入口，不是另起平台协议，也不自动填充真实目录。

## 实质限制与真实验收

原生请求结构参考 [yt-dlp Bilibili 提取器](https://github.com/yt-dlp/yt-dlp/blob/master/yt_dlp/extractor/bilibili.py)，字幕证据参考 [字幕 API 参数追溯](https://github.com/gcnyin/bilibili-subtitle-api)。本轮仅读取公开开发资料，没有请求实际 Bilibili 账号/内容。接口变化、WBI、账号权限、付费/地区限制、收藏夹/合集大规模扫描和实际 XML 弹幕仍需获准后在 Y7000P 实测。

电影各版本按平台真实单元返回，不伪造 P；依据实际 DASH audio 的 language/language_doc 等字段按语言保留最高可用音轨，形成 LANGUAGE 单元并保留共享视频身份，字段依据见 [BBDown Rust 平台解析器](https://github.com/Joey-Project/BBDown-rust/blob/master/crates/bbdown/src/client.rs)。未提供语言的音轨不猜标签。额外 `dubbing_info` 结构的映射没有本轮可验证证据，遇到它报告 ALTERNATE_AUDIO_METADATA_UNSUPPORTED。非 DASH/DRM/未知媒体结构不伪造可下载轨道。账号登录/session 装载、云盘挂载探测、上传及云端核验没有在本轮实现或宣称成功。

本地替身通过不等于 Java 跨进程组合、前端视觉、真实平台、Y7000P 部署或七天稳定性验收。后端集成归 Java 对话，读取本分支明确提交后在其工作树合入；不合并 main，不触碰旧目录/服务。
