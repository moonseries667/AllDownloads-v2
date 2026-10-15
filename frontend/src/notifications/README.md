# 第二部分通知与 Telegram 接入

本模块消费第二部分契约 `6233234`；App 布局来自第一部分已验证提交 `38502f8`。TelegramPage 只使用收藏增量、监控、补齐、任务取消和已确认资格的收藏单元接口，不再请求链接下载或资源库。

第二部分前端二统一修改共享 API 时需完成：

- `api/client.ts` 的 `notifyError` / `notifySuccess` 转发到 `notifications/notify.ts`。所有页面沿用原导入即可共用同一入口，删除旧 `recentErrors` 和直接 `ElNotification` 实现。成功标题可继续按 isDemo 传第二个参数；warning/info 用 `notifyWarning` 或 `notify`。不要同时调用新旧弹窗。
- `/telegram/messages` 与详情 mock 遵循后端：`eligible:true`、稳定 `id/groupId/messageIds`，包括混合组全部实际附件。纯文本、纯照片、纯普通文件、照片加普通文件从结果和 total 中排除。页面仅校验资格标记，不自行拼组或用扩展名判断。
- `/issues` mock 更新为 `ServerIssue`（code/message/severity/occurredAt/updatedAt/read/resolved 等，字段见 store.ts）。App 通过 `readAttentionIssues` 完整分页后一次替换服务器快照。读取、关闭与已读不解决问题；查询失败保留旧快照。
- 原来只有 `/telegram?tab=resources` 一类查询参数入口，当前页面自然回到收藏夹。主路由没有旧子路径；若增加旧路径兼容，统一重定向 `/telegram`。

`NotificationCenter :issues="ServerIssue[]"` 放在 App 顶栏。`notify({title,message,type,key?,href?,action?})` 记录所有非 success 提示；稳定 key 优先用于同一对象同一问题，未提供时按页面/严重程度/标题/正文去重。点击本体或关闭图标只关弹窗；独立 action 阻止冒泡。客户端记录在当前应用会话中保留，后台问题在刷新后重新查询恢复，不把正文写入浏览器持久存储。

隔离验证：`npm test`、`npm run build`、`npx playwright test --config tests/telegram-notifications.playwright.ts`。浏览器测试通过 HTTP fixture 返回契约数据，不使用账号或平台 I/O。默认 demo 的资格与通知事实由共享 API 对话接入后再做整体集成验证。
