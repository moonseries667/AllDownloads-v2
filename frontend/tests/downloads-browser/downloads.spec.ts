import { expect, test, type Page } from "@playwright/test";
const url = "/tests/hosts/downloads.html#/downloads";
const row = (page: Page, id: string) => page.locator(`[data-project-id="${id}"]`);
const part = (page: Page, id: string) => page.locator(`[data-part-id="${id}"]`);
async function control(page: Page, action: string, ...args: string[]) {
  await page.evaluate(({ action, args }) => { (window as any).downloadTest[action](...args); (window as any).downloadTest.refresh(); }, { action, args });
}

test("column handles toggle fit/minimum, drag resets fit, sort stays independent and keyboard works", async ({ page }) => {
  await page.goto(url);
  const handle = page.getByRole("separator", { name: "调整下载内容列宽" });
  await expect(row(page, "bil-new")).toBeVisible();
  const header = page.getByRole("columnheader").first();
  await page.getByRole("button", { name: "下载内容" }).click();
  await expect(header).toHaveAttribute("aria-sort", "ascending");
  await handle.dblclick(); const fitted = Number(await handle.getAttribute("aria-valuenow"));
  expect(fitted).toBeGreaterThanOrEqual(240);
  await handle.dblclick(); await expect(handle).toHaveAttribute("aria-valuenow", "240");
  await handle.dblclick(); await expect(handle).toHaveAttribute("aria-valuenow", String(fitted));
  const box = (await handle.boundingBox())!;
  await page.mouse.move(box.x + 5, box.y + 15); await page.mouse.down(); await page.mouse.move(box.x + 95, box.y + 15, { steps: 6 }); await page.mouse.up();
  expect(Number(await handle.getAttribute("aria-valuenow"))).toBe(fitted + 90);
  await handle.dblclick(); await expect(handle).toHaveAttribute("aria-valuenow", String(fitted));
  await expect(header).toHaveAttribute("aria-sort", "ascending");
  await handle.focus(); await page.keyboard.press("Home"); await expect(handle).toHaveAttribute("aria-valuenow", "240");
  await page.keyboard.press("ArrowRight"); await expect(handle).toHaveAttribute("aria-valuenow", "250");
  await page.keyboard.press("Enter"); await expect(handle).toHaveAttribute("aria-valuenow", String(fitted));
  await page.getByRole("button", { name: "调整列宽", exact: true }).click();
  await page.getByRole("button", { name: "下载内容缩窄", exact: true }).click();
  await expect(handle).toHaveAttribute("aria-valuenow", String(Math.max(240, fitted - 20)));
});

test("stable content filter survives reload and browser return, empty history never downloads", async ({ page }) => {
  await page.goto(url + "?platform=BILIBILI&contentId=b1");
  await expect(page.locator("tbody tr[data-project-id]")).toHaveCount(2);
  await expect(row(page, "bil-other")).toHaveCount(0);
  await page.reload(); await expect(row(page, "bil-new")).toBeVisible();
  await page.getByRole("button", { name: "清除定向筛选" }).click();
  await expect(page).not.toHaveURL(/contentId/);
  await page.goBack(); await expect(page).toHaveURL(/contentId=b1/);
  await expect(page.getByText("当前内容的全部下载记录")).toBeVisible();
  await page.goto(url + "?platform=BILIBILI&contentId=no-history");
  await expect(page.getByText("此内容暂无下载记录。查看记录不会发起下载。")).toBeVisible();
  expect(await page.evaluate(() => (window as any).downloadTest.requests.filter((r: any) => r.method === "POST"))).toEqual([]);
});

test("pagination keeps each logical message and share as a project", async ({ page }) => {
  await page.goto(url);
  await page.getByRole("button", { name: "加载更多下载项目" }).click();
  await expect(page.locator("tbody tr[data-project-id]")).toHaveCount(4);
  await page.getByRole("button", { name: "加载更多下载项目" }).click();
  await expect(page.locator("tbody tr[data-project-id]")).toHaveCount(6);
  await expect(row(page, "tg-mixed")).toContainText("saved-scan-1");
  await expect(row(page, "tg-other")).toContainText("saved-scan-1");
  await expect(row(page, "quark")).toContainText("0 / 2");
  await page.getByRole("textbox", { name: "搜索下载内容" }).fill("摄影课程");
  await page.getByRole("textbox", { name: "搜索下载内容" }).press("Enter");
  await expect(page.locator("tbody tr[data-project-id]")).toHaveCount(1);
  await expect(row(page, "quark")).toBeVisible();
});

test("both Bilibili detail entry points link the stable content ID", async ({ page }) => {
  await page.goto(url);
  await page.getByRole("button", { name: "新版详情测试入口" }).click();
  await expect(page.getByRole("link", { name: "查看下载记录" })).toHaveAttribute("href", "/downloads?platform=BILIBILI&contentId=b1");
  await page.getByRole("button", { name: "关闭", exact: true }).click();
  await page.getByRole("link", { name: "Bilibili 测试入口" }).click();
  await page.getByRole("textbox", { name: "搜索视频标题", exact: true }).fill("把镜头交给山野");
  const card = page.locator(".video-card").filter({ hasText: "把镜头交给山野" });
  await card.getByRole("button", { name: "详情", exact: true }).click();
  await page.getByRole("button", { name: "查看下载记录", exact: true }).click();
  await expect(page).toHaveURL(/platform=BILIBILI&contentId=b1/);
  await expect(page.getByText("当前内容的全部下载记录")).toBeVisible();
});

test("Telegram mixed album photo cancellation waits for confirmation and keeps audio/video independent", async ({ page }) => {
  await page.goto(url + "?platform=TELEGRAM");
  await row(page, "tg-mixed").getByRole("button", { name: "查看详情" }).click();
  await expect(part(page, "tg-photo")).toContainText("现场照片.jpg");
  await part(page, "tg-photo").getByRole("button", { name: "取消此文件" }).click();
  await expect(part(page, "tg-photo").getByRole("button", { name: "正在取消" })).toBeDisabled();
  await expect(part(page, "tg-audio").getByRole("button", { name: "取消此文件" })).toBeEnabled();
  await expect(part(page, "tg-video")).not.toContainText("已取消");
  await control(page, "finish", "tg-mixed", "tg-photo", "CANCELLED");
  await control(page, "finish", "tg-mixed", "tg-video", "SUCCEEDED");
  await expect(part(page, "tg-photo")).toContainText("已取消");
  await expect(part(page, "tg-video")).toContainText("已验证");
  await expect(part(page, "tg-audio")).toContainText("进行中");
  await expect(page.locator(".detail-summary")).toContainText("已完成 1 / 3");
  await expect(row(page, "tg-other")).not.toContainText("正在取消");
  expect(await page.evaluate(() => (window as any).downloadTest.requests.filter((r: any) => r.method === "POST").length)).toBe(1);
  await page.locator(".el-drawer__body").evaluate(el => el.scrollTop = 0);
  await page.screenshot({ path: test.info().outputPath("telegram-cancellation.png"), fullPage: true });
});

for (const [platform, id] of [["BILIBILI", "bil-new"], ["TELEGRAM", "tg-mixed"], ["QUARK", "quark"]]) {
  test(`${platform} top-level cancel retains row and acknowledged intent across reload`, async ({ page }) => {
    await page.goto(url + "?platform=" + platform);
    await row(page, id!).getByRole("button", { name: "取消任务" }).click();
    await expect(row(page, id!).getByRole("button", { name: "正在取消" })).toBeDisabled();
    await page.reload();
    await expect(row(page, id!).getByRole("button", { name: "正在取消" })).toBeDisabled();
    if (platform === "BILIBILI") await expect(row(page, "bil-old").getByRole("button", { name: "取消任务" })).toBeEnabled();
  });
}

test("Bilibili part explains merge impact and preserves completed sibling; Quark file cancellation is independent", async ({ page }) => {
  await page.goto(url + "?platform=BILIBILI&contentId=b1");
  await row(page, "bil-new").getByRole("button", { name: "查看详情" }).click();
  await expect(part(page, "p1")).toContainText("一并停止其音视频下载和合并");
  await part(page, "p1").getByRole("button", { name: "取消此部分" }).click();
  await expect(part(page, "p2")).toContainText("已验证");
  await control(page, "finish", "bil-new", "p1", "CANCELLED");
  await expect(page.locator(".detail-summary")).toContainText("已完成 1 / 2");
  await page.goto(url + "?platform=QUARK");
  await row(page, "quark").getByRole("button", { name: "查看详情" }).click();
  await part(page, "q1").getByRole("button", { name: "取消此文件" }).click();
  await expect(part(page, "q2").getByRole("button", { name: "取消此文件" })).toBeEnabled();
});

test("request failure is recoverable and completion race shows real successful files", async ({ page }) => {
  await page.goto(url + "?platform=BILIBILI&contentId=b1");
  await control(page, "failNext", "error");
  await row(page, "bil-old").getByRole("button", { name: "取消任务" }).click();
  await expect(page.getByText(/连接中断，取消未能确认/).first()).toBeVisible();
  await expect(row(page, "bil-old").getByRole("button", { name: "取消任务" })).toBeEnabled();
  await control(page, "failNext", "race");
  await row(page, "bil-old").getByRole("button", { name: "取消任务" }).click();
  await expect(row(page, "bil-old")).toContainText("已完成");
  await expect(row(page, "bil-old")).not.toContainText("正在取消");
});

test("desktop widths keep overflow within table and controls visible", async ({ page }) => {
  const errors: string[] = []; page.on("pageerror", e => errors.push(e.message));
  await page.goto(url + "?platform=TELEGRAM");
  for (const [width, height] of [[1280,720], [1440,900], [1920,1080], [1024,576], [1152,720], [1536,864]]) {
    await page.setViewportSize({ width: width!, height: height! });
    await expect(row(page, "tg-mixed")).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
    await page.getByRole("separator", { name: "调整操作列宽" }).focus(); await page.keyboard.press("Home");
    await expect(row(page, "tg-mixed").getByRole("button", { name: "取消任务" })).toBeVisible();
  }
  expect(errors).toEqual([]);
  await page.screenshot({ path: test.info().outputPath("downloads-desktop.png"), fullPage: true });
});
