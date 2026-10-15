import { test, expect, type Page } from "@playwright/test";
import { createSeed } from "../../src/api/fixtures";

const key = "alldownloads.demo.feedback2.v3";
async function seed(page: Page, state = createSeed()) {
  await page.addInitScript(({ key, state }) => {
    if (!localStorage.getItem(key)) localStorage.setItem(key, JSON.stringify(state));
  }, { key, state });
}
async function previewFolder(page: Page) {
  await page.goto("/quark/cloud");
  await page.locator(".quark-table .el-table__body tbody tr").filter({ hasText: "摄影课程" }).getByRole("checkbox").check();
  await page.getByRole("button", { name: "预览下载", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "确认下载清单" });
  await expect(dialog.getByRole("button", { name: "提交下载", exact: true })).toBeEnabled();
  return dialog;
}
test("Quark shared preview sorts real sizes and freezes a local-only batch without changing cloud targets", async ({ page }) => {
  await seed(page);
  await page.setViewportSize({ width: 1024, height: 576 });
  const dialog = await previewFolder(page);
  await expect(dialog).toContainText("下载到...");
  await expect(dialog.getByRole("columnheader", { name: "大小", exact: true })).toBeVisible();
  await expect(dialog).not.toContainText("相对目录");
  const titles = dialog.locator(".preview-title");
  await expect(titles).toHaveText(["01 光线的方向.mp4", "02 构图练习.mp4"]);
  await dialog.getByRole("button", { name: "标题，当前正序，点击切换" }).click();
  await expect(titles).toHaveText(["02 构图练习.mp4", "01 光线的方向.mp4"]);
  await dialog.getByRole("button", { name: "修改目录" }).click();
  await dialog.getByLabel("产品运行主机的本机目录").fill("D:\\QuarkBatchOnly");
  await dialog.getByRole("button", { name: "校验并使用此目录" }).click();
  await expect(dialog.getByRole("button", { name: "提交下载", exact: true })).toBeDisabled();
  await expect(dialog).toContainText("仅本批下载到指定目录，不进入归档");
  await dialog.getByRole("button", { name: "刷新", exact: true }).click();
  await expect(dialog.getByRole("button", { name: "提交下载", exact: true })).toBeEnabled();
  const buttons = dialog.locator(".el-dialog__footer .el-button");
  for (const button of await buttons.all()) {
    const box = await button.boundingBox();
    expect(box!.y).toBeGreaterThanOrEqual(0);
    expect(box!.y + box!.height).toBeLessThanOrEqual(576);
  }
  await page.screenshot({ path: "test-results/quark-preview-desktop.png", fullPage: true });
  await dialog.getByRole("button", { name: "提交下载", exact: true }).click();
  const state = await page.evaluate((key) => JSON.parse(localStorage.getItem(key)!), key);
  expect(state.orders[0]).toMatchObject({ platform: "QUARK", archivePolicy: "NONE", target: { path: "D:\\QuarkBatchOnly" } });
  expect(state.settings.downloadRoot).toBe("D:\\AllDownloadsData");
  expect(state.shares[0]).toMatchObject({ targetFolderId: "folder-learning", targetPath: "学习资料" });
});

test("Quark shared preview hides the size column if any file size is unknown", async ({ page }) => {
  const state = createSeed();
  state.nodes.find((node) => node.id === "q1-sub1")!.sizeBytes = null;
  await seed(page, state);
  const dialog = await previewFolder(page);
  await expect(dialog.getByRole("columnheader", { name: "大小", exact: true })).toHaveCount(0);
});

test("shared API failures stay in attention after toast dismissal and navigation", async ({ page }) => {
  const state = createSeed();
  state.scenario = "disk";
  await seed(page, state);
  const dialog = await previewFolder(page);
  await dialog.getByRole("button", { name: "提交下载", exact: true }).click();
  await page.getByRole("button", { name: "关闭提示：本机剩余空间不足，请释放空间后重新提交", exact: true }).click();
  await expect(page.locator(".el-notification")).toHaveCount(0);
  await dialog.getByRole("button", { name: "返回选择", exact: true }).click();
  await page.locator(".sidebar").getByRole("link", { name: "Telegram", exact: true }).click();
  await page.getByRole("button", { name: "通知", exact: true }).click();
  await expect(page.locator(".attention-notice").filter({ hasText: "本机剩余空间不足" })).toHaveCount(1);
  await expect(page.locator(".attention-notice")).toHaveCount(3);
});

test("integrated Telegram demo returns only eligible saved units and all mixed attachments", async ({ page }) => {
  await seed(page);
  await page.goto("/telegram?tab=resources");
  await expect(page.getByRole("heading", { name: "收藏夹", exact: true })).toBeVisible();
  await expect(page.locator(".message-card")).toHaveCount(2);
  await expect(page.getByRole("button", { name: "资源库", exact: true })).toHaveCount(0);
  await page.locator(".message-card").first().getByRole("button", { name: "展开消息正文和附件" }).click();
  await expect(page.locator(".message-card").first().locator(".attachment-row")).toHaveCount(4);
  await expect(page.locator(".message-card").first()).toContainText("同组山间照片.jpg");
  await page.getByRole("button", { name: "通知", exact: true }).click();
  await expect(page.locator(".attention-notice")).toHaveCount(2);
  await page.reload();
  await page.getByRole("button", { name: "通知", exact: true }).click();
  await expect(page.locator(".attention-notice")).toHaveCount(2);
});
