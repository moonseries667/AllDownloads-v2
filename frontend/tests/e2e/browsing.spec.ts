import { test, expect } from "@playwright/test";

test("card adds once; title and independent controls do not bubble", async ({ page, context }) => {
  await page.goto("/bilibili");
  const card = page.locator(".video-card").first();
  await card.locator(".video-cover img").click();
  await card.locator(".video-author").click();
  await expect(page.locator(".selection-bar")).toContainText("已选择 1 项");
  await card.locator(".cover-checkbox").click();
  await expect(page.locator(".selection-bar")).toHaveCount(0);
  // Abort the external request: this verifies the target without accessing a platform account.
  await context.route("https://www.bilibili.com/**", route => route.abort());
  const popup = page.waitForEvent("popup");
  await card.locator(".video-title").click();
  await (await popup).close();
  await expect(page.locator(".selection-bar")).toHaveCount(0);
  await card.getByRole("button", { name: "详情", exact: true }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await expect(page.locator(".selection-bar")).toHaveCount(0);
});

test("source grouping preserves identity and pin persists with visible feedback", async ({ page }) => {
  await page.goto("/bilibili");
  const other = page.getByRole("region", { name: "其他", exact: true });
  await expect(other.getByRole("heading", { name: "用户添加视频" })).toBeVisible();
  await expect(other.getByRole("heading", { name: "电影", exact: true })).toBeVisible();
  await expect(other.getByRole("heading", { name: "番剧", exact: true })).toBeVisible();
  await page.getByRole("button", { name: /^设置来源/ }).first().click();
  const pin = page.getByRole("switch", { name: "置顶来源" });
  const wasChecked = await pin.isChecked();
  await page.locator(".setting-inline .el-switch").click();
  await expect(page.getByRole("dialog")).toContainText(wasChecked ? "已取消置顶，刷新后保留" : "已置顶，刷新后保留");
  await page.getByRole("button", { name: "取消", exact: true }).click();
  await page.reload();
  await page.getByRole("button", { name: /^设置来源/ }).first().click();
  if (wasChecked) await expect(pin).not.toBeChecked(); else await expect(pin).toBeChecked();
});

test("date calendar is themed, clearable and returns keyboard focus", async ({ page }) => {
  await page.goto("/bilibili");
  const start = page.getByRole("combobox", { name: "发布时间起始日期" });
  await start.click();
  await expect(page.locator(".published-calendar:visible")).toBeVisible();
  await start.press("Escape");
  await expect(page.locator(".published-calendar:visible")).toHaveCount(0);
  await expect(start).toBeFocused();
  await start.fill("2026-10-01");
  await start.press("Enter");
  await expect(start).toHaveValue("2026-10-01");
  await page.getByRole("button", { name: "清除日期", exact: true }).click();
  await expect(start).toHaveValue("");
});

test("workspace navigation scrolls at short desktop heights and 125/150 percent equivalent", async ({ page }) => {
  await page.goto("/bilibili");
  for (const [width, height] of [[1440, 900], [1152, 576], [960, 480], [1280, 420]]) {
    await page.setViewportSize({ width, height });
    const settings = page.locator(".sidebar-bottom").getByRole("link", { name: "设置与账号" });
    await page.locator(".sidebar nav").getByRole("link", { name: /任务与问题/ }).focus();
    await page.keyboard.press("Tab");
    await expect(settings).toBeInViewport();
    await expect(settings).toBeFocused();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
    expect(await page.evaluate(() => window.scrollY)).toBe(0);
  }
});

test("pin failure restores persisted state and keeps a useful reason", async ({ page }) => {
  await page.goto("/settings");
  await page.locator(".demo-controls .el-select__wrapper").click();
  await page.getByRole("option", { name: "连接中断", exact: true }).click();
  await page.getByRole("button", { name: "加载场景", exact: true }).click();
  await page.goto("/bilibili");
  await page.getByRole("button", { name: /^设置来源/ }).first().click();
  const pin = page.getByRole("switch", { name: "置顶来源" });
  const before = await pin.isChecked();
  await page.locator(".setting-inline .el-switch").click();
  await expect(page.getByRole("dialog").getByRole("alert")).toBeVisible();
  if (before) await expect(pin).toBeChecked(); else await expect(pin).not.toBeChecked();
});
