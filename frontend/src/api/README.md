# 前端数据接入

本目录的 Bilibili 类型消费 Java 已提交契约 `d508c7a`，`feedback.generated.ts` 从该提交生成。重新生成命令在仓库根运行：

```powershell
node frontend/src/api/generate-feedback-contract.mjs d508c7a
```

`bilibiliApi` 提供详情、列表、来源读取与置顶更新；排序使用 `TITLE_ASC / TITLE_DESC / PUBLISHED_DESC / PUBLISHED_ASC`，日期为上海时区的 `yyyy-MM-dd`。详情和列表的作者是 `author: {id,name} | null`，内容类型是 `kind: SINGLE / MULTIPART / SERIES / MOVIE`；不要继续读演示旧字段 `type` 或把 `author` 当字符串。`overview.runtime` 与 `overview.bilibili` 按同一冻结契约提供。演示运行状态为 UNKNOWN，不代表实际服务或挂载正常。

宿主页接入 `BilibiliDetail.vue`：传 `v-model` 和 `content-id: string | null`，组件自行读取详情。`saved` 事件携带 `{contentId, contentVersion, mediaIds}`，`close` 表示抽屉关闭；保存不会预览或提交下载。共享 `mediaSelectionSession` 按稳定内容 ID 保存会话选择，详情关闭未保存时丢弃草稿。空数组是明确空选择，不恢复全选。

`DownloadPreview.vue` 保持原 `v-model / platform / selection / submitted` 接口，自动把已保存媒体转换成契约 `mediaSelections`。宿主页传实际已选的内容 ID；跨来源、排序与分页不应清理会话选择。已加入清单内容再次保存时，打开的预览立即失效；返回选择后再打开会重新读取已保存选项。未自定义的内容不附加媒体选择，由 Java 展开全部可下载项。

目录通过 `POST /download-targets/validate` 和 `GET /download-targets/{id}` 校验，只有 VALID 目标引用可以进入预览。本批目录或媒体改变后必须刷新；提交只发送 `previewId`。取消目录编辑不改变当前目标，返回选择保留本批目标，成功提交后下批恢复默认。生产路径由 Java 返回；`demo-bilibili.ts` 中的路径仅是隔离替身数据。

演示数据使用新键 `alldownloads.demo.feedback2.v3`，旧演示缓存保留但不继续读取。媒体选择保存在 sessionStorage 的 `alldownloads.media-selection.v1`；不会保存凭据。大小未知为 null，人工/AI/未分类字幕按契约区分。Telegram 与夸克按第二部分 `6233234` / `9e7886f` 接入，目录与预览仍使用第一部分的公共目标校验和冻结提交。

在 frontend 目录验证：

```powershell
npm test
npm run build
node node_modules/@playwright/test/cli.js test --config tests/media.playwright.config.ts
```

专属浏览器测试使用 5186 端口的独立测试宿主，不修改宿主页。验证详情保存/取消/空选、折叠与语言、批次目录、旧预览失效、冻结提交和较矮桌面键盘可达性。均为 Carbon 上的 mock/组件验证；未证明 Java/Go 真实联调、源站下载、磁盘校验或云端归档，最终视觉由用户验收。

## 第二部分共享接入

`quark-folders.ts` 消费 Java 的 QuarkFolder/分页模型，根节点由服务器返回，不假定根 ID；显示路径取真实父链名称，提交 id+revision。`quark-folder-fixtures.ts` 只提供隔离目录与权限/过期替身，`demo-quark.ts` 向公共预览提供大小、状态、目标和文件快照。云盘转存目标与本机 DownloadTarget 分离。

`telegram.ts` 只接受后端标记 eligible=true 的完整收藏单元。共享 mock 提供合格混合组的全部附件，以及被排除的纯文本/纯照片/纯普通文件测试事实；客户端不自行分类或拼接相邻消息。旧链接下载和 Telegram 手动清单入口返回 FEATURE_REMOVED。

`client.ts` 的默认 api 对全部请求失败调用统一通知入口；各页面继续从 client 导入 notifyError/notifySuccess。失败保持原异常供页面内联处理，通知模块按稳定键去重。GET /issues mock 返回稳定 issue id、读/解决状态和时间；acknowledge 或 retry 不解决问题。此 mock 仅验证契约和 UI，不执行后台扫描、下载或恢复。
