import { test, expect, type Page } from "@playwright/test";

const fullText = "一段需要展开阅读的收藏正文。".repeat(16) + "\n第二行完整说明与链接 https://example.com/reference";
const message = {
  id: "group7", externalId: "31", eligible: true, groupId: "album7", messageIds: ["31", "32"],
  text: fullText, sentAt: "2026-10-15T09:00:00Z", sourceLinks: [], downloadSummary: { state: "PARTIAL" },
  attachments: [
    { id: "v", name: "视频.mp4", mediaType: "VIDEO", sizeBytes: "123", downloadState: "VERIFIED" },
    { id: "p", name: "同组照片.jpg", mediaType: "PHOTO", sizeBytes: "45", downloadState: "NEEDS_ACTION", error: { message: "照片下载中断" } },
    { id: "d", name: "同组说明.pdf", mediaType: "DOCUMENT", sizeBytes: null, downloadState: "QUEUED" },
  ],
};
const serverIssue = {
  id: "issue7", taskId: "task7", objectId: "group7", code: "DOWNLOAD_INTERRUPTED", message: "照片下载中断",
  severity: "ERROR", occurredAt: "2026-10-15T09:00:00Z", updatedAt: "2026-10-15T09:00:00Z", read: true, resolved: false,
};
async function fixture(page: Page, options: { empty?: boolean; failSync?: boolean; paged?: boolean } = {}) {
  const requests: string[] = [];
  await page.route("**/api/v1/**", async route => {
    const url = new URL(route.request().url());
    const path = url.pathname.replace("/api/v1", "");
    requests.push(route.request().method() + " " + path);
    let data: any = { items: [], total: 0, nextCursor: null };
    if (path === "/overview") data = { activeTasks: 0, issues: 1 };
    if (path === "/issues") data = { items: [serverIssue], total: 1, nextCursor: null };
    if (path === "/telegram/watcher") data = {
      accountId: "tg-main", enabled: false, intervalSeconds: 60, revision: "1", lastCheckedAt: null, nextCheckAt: null,
      scanState: { initialScanCompleted: false, cursorMessageId: "0", activeTaskId: null, scannedThroughAt: null },
    };
    if (path === "/telegram/messages") {
      const nextPage = url.searchParams.has("cursor");
      data = { items: options.empty ? [] : [nextPage ? { ...message, id: "next", groupId: null, text: "第二页音频" } : message],
        total: options.empty ? 0 : options.paged ? 2 : 1, nextCursor: options.paged && !nextPage ? "page2" : null };
    }
    if (path.startsWith("/telegram/messages/")) data = message;
    if (path === "/telegram/saved-sync" && options.failSync) {
      return route.fulfill({ status: 503, json: { error: { code: "RUNNER_UNAVAILABLE", message: "执行程序暂不可用" }, requestId: "test" } });
    }
    if (route.request().method() === "POST") data = { taskId: "task-new" };
    await route.fulfill({ json: { data, requestId: "test" } });
  });
  return requests;
}
async function notice(page: Page, input: Record<string, unknown>) {
  await page.evaluate(async input => {
    const modulePath = "/src/notifications/notify.ts";
    const { notify } = await import(/* @vite-ignore */ modulePath);
    notify(input);
  }, input);
}
test("saved view removes old entries and expands body in place with all group attachments", async ({ page }) => {
  const requests = await fixture(page);
  await page.goto("/telegram?tab=resources&q=正文");
  await expect(page.getByRole("heading", { name: "收藏夹", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "资源库", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "消息链接下载", exact: true })).toHaveCount(0);
  await expect(page.getByRole("textbox", { name: "搜索消息正文" })).toHaveValue("正文");
  const card = page.locator(".message-card");
  await card.locator(".message-preview").click();
  await expect(card.locator(".message-preview")).toHaveCount(0);
  await expect(card.getByLabel("完整消息正文")).toHaveText(fullText);
  await expect(card.locator(".attachment-row")).toHaveCount(3);
  await expect(card).toContainText("同组照片.jpg");
  await expect(card).toContainText("同组说明.pdf");
  await expect(card).toContainText("照片下载中断");
  await card.getByRole("button", { name: "收起消息正文和附件" }).click();
  await expect(card.locator(".message-preview")).toBeVisible();
  await expect(card.getByLabel("完整消息正文")).toHaveCount(0);
  expect(requests.some(r => r.includes("resources") || r.includes("link-downloads") || r.startsWith("POST"))).toBe(false);
});
test("empty eligible results remain a normal state and pagination preserves groups", async ({ page }) => {
  await fixture(page, { empty: true });
  await page.goto("/telegram");
  await expect(page.getByText("没有符合条件的收藏内容")).toBeVisible();
  await expect(page.locator(".telegram-page-alert")).toHaveCount(0);
  await page.unrouteAll();
  await fixture(page, { paged: true });
  await page.reload();
  await page.getByRole("button", { name: "加载更多", exact: true }).click();
  await expect(page.locator(".message-card")).toHaveCount(2);
  await expect(page.getByRole("button", { name: "加载更多", exact: true })).toHaveCount(0);
});
test("failed sync popup closes on body click and one failure survives navigation", async ({ page }) => {
  const requests = await fixture(page, { failSync: true });
  await page.goto("/telegram");
  await page.getByRole("button", { name: "下载收藏增量", exact: true }).click();
  await expect(page.getByRole("dialog")).toContainText("同组照片");
  await page.getByRole("button", { name: "确认并开始扫描" }).click();
  const toast = page.locator(".el-notification");
  await expect(toast).toContainText("执行程序暂不可用");
  await toast.getByRole("button", { name: "关闭提示：执行程序暂不可用" }).click();
  await expect(toast).toHaveCount(0);
  await page.getByRole("button", { name: "确认并开始扫描" }).click();
  await page.getByRole("button", { name: "稍后", exact: true }).click();
  await page.locator(".sidebar").getByRole("link", { name: "下载管理" }).click();
  await page.getByRole("button", { name: "通知", exact: true }).click();
  await expect(page.locator(".attention-notice").filter({ hasText: "执行程序暂不可用" })).toHaveCount(1);
  await expect(page.locator(".attention-notice").filter({ hasText: "照片下载中断" })).toHaveCount(1);
  expect(requests.filter(r => r.includes("acknowledge") || r.includes("/actions"))).toEqual([]);
});
test("warnings and informational notices persist, success does not, action does not dismiss", async ({ page }) => {
  await fixture(page);
  await page.goto("/telegram");
  for (const type of ["warning", "info"]) {
    await notice(page, { type, title: "需要关注", message: "需要处理-" + type, key: type });
    await page.locator(".el-notification").filter({ hasText: "需要处理-" + type }).getByRole("heading").click();
  }
  await notice(page, { type: "success", title: "已受理", message: "正常成功提示" });
  await page.locator(".el-notification").filter({ hasText: "正常成功提示" }).getByRole("heading").click();
  await page.getByRole("button", { name: "通知", exact: true }).click();
  await expect(page.locator(".attention-notice")).toHaveCount(3);
  await expect(page.locator(".notification-center")).not.toContainText("正常成功提示");
  await page.getByRole("button", { name: "通知", exact: true }).click();
  await page.evaluate(async () => {
    const modulePath = "/src/notifications/notify.ts";
    const { notify } = await import(/* @vite-ignore */ modulePath);
    notify({ type: "warning", title: "独立动作", message: "动作待处理", action: { label: "查看", run: () => { document.title = "动作已执行"; } } });
  });
  await page.locator(".el-notification").getByRole("button", { name: "查看", exact: true }).click();
  await expect(page).toHaveTitle("动作已执行");
  await expect(page.locator(".el-notification").filter({ hasText: "动作待处理" })).toBeVisible();
});
test("read server problems restore on reload and the desktop view has no overflow", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", error => errors.push(error.message));
  await fixture(page);
  for (const [width, height] of [[1280,720], [1024,576], [1440,900]]) {
    await page.setViewportSize({ width, height });
    await page.goto("/telegram");
    await page.getByRole("button", { name: "展开消息正文和附件" }).click();
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
    await page.getByRole("button", { name: "通知", exact: true }).click();
    await expect(page.locator(".attention-notice").filter({ hasText: "照片下载中断" })).toHaveCount(1);
  }
  await page.screenshot({ path: "test-results/telegram-notifications.png", fullPage: true });
  expect(errors).toEqual([]);
});
