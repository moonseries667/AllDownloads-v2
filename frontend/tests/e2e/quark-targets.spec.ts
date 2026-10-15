import { test, expect, type Page } from "@playwright/test";

async function addShares(page: Page) {
  await page.goto("/quark/cloud");
  await page.getByRole("button", { name: "添加分享转存", exact: true }).click();
  await page.getByRole("textbox", { name: "夸克分享链接，每行一条" }).fill("https://pan.quark.cn/s/FolderFirst2026\nhttps://pan.quark.cn/s/FolderSecond2026");
}
async function choose(page: Page, field: string, names: string[]) {
  await page.getByRole("button", { name: field, exact: true }).click();
  const browser = page.getByRole("dialog", { name: "选择夸克文件夹", exact: true });
  await browser.getByRole("button", { name: "我的云盘", exact: true }).click();
  for (const name of names) await browser.getByRole("button", { name, exact: true }).click();
  await browser.getByRole("button", { name: "使用此文件夹", exact: true }).click();
  await expect(browser).not.toBeVisible();
}
async function storedShares(page: Page) {
  return page.evaluate(() => JSON.parse(localStorage.getItem("alldownloads.demo.v1")!).shares.filter((share: { externalShareId: string }) => share.externalShareId.startsWith("Folder")));
}

test("Quark navigates the actual root and nested folders, then freezes one target for the batch", async ({ page }) => {
  await addShares(page);
  const submit = page.getByRole("button", { name: "转存并维护更新", exact: true });
  await expect(submit).toBeDisabled();
  await page.getByRole("button", { name: "本批转存目标夸克文件夹" }).click();
  const browser = page.getByRole("dialog", { name: "选择夸克文件夹", exact: true });
  await expect(browser.locator("input")).toHaveCount(0);
  await expect(browser.getByRole("button", { name: "上一级" })).toBeDisabled();
  await expect(browser.getByRole("list", { name: "子文件夹" }).getByRole("button")).toHaveText(["学习资料", "收件箱", "共享资料"]);
  for (const name of ["学习资料", "课程", "摄影"]) await browser.getByRole("button", { name, exact: true }).click();
  await expect(browser.locator(".folder-current")).toHaveText("当前文件夹：学习资料/课程/摄影");
  await expect(browser).toContainText("此文件夹内没有子文件夹");
  await browser.getByRole("button", { name: "上一级" }).click();
  await expect(browser.locator(".folder-current")).toHaveText("当前文件夹：学习资料/课程");
  await browser.getByRole("button", { name: "使用此文件夹" }).click();
  const orderCount = await page.evaluate(() => JSON.parse(localStorage.getItem("alldownloads.demo.v1")!).orders.length);
  await submit.click();
  await expect.poll(() => storedShares(page)).toMatchObject([{ targetFolderId: "folder-course", targetFolderRevision: "1" }, { targetFolderId: "folder-course", targetFolderRevision: "1" }]);
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem("alldownloads.demo.v1")!).orders.length)).toBe(orderCount);
});

test("Quark switches between batch and per-entry targets without reusing hidden values", async ({ page }) => {
  await addShares(page);
  await choose(page, "本批转存目标夸克文件夹", ["学习资料", "课程"]);
  const toggle = page.getByRole("switch", { name: "是否整批使用同一目录" });
  await toggle.click();
  await expect(page.getByRole("button", { name: "第 1 条分享的目标文件夹" })).toContainText("学习资料/课程");
  await choose(page, "第 2 条分享的目标文件夹", ["收件箱", "课程"]);
  await toggle.click();
  await expect(page.getByRole("button", { name: "转存并维护更新" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "本批转存目标夸克文件夹" })).toContainText("选择夸克文件夹");
  await choose(page, "本批转存目标夸克文件夹", []);
  await toggle.click();
  await expect(page.getByRole("button", { name: "第 2 条分享的目标文件夹" })).toContainText("我的云盘");
  await choose(page, "第 1 条分享的目标文件夹", ["学习资料", "课程"]);
  await page.getByRole("button", { name: "转存并维护更新" }).click();
  await expect.poll(() => storedShares(page)).toMatchObject([{ targetFolderId: "folder-course", targetFolderRevision: "1" }, { targetFolderId: "0", targetFolderRevision: "1" }]);
});

test("Quark read-only directories cannot be selected; compact desktop keeps the switch inline and footer spaced", async ({ page }) => {
  await page.setViewportSize({ width: 1024, height: 576 });
  await addShares(page);
  const row = page.locator(".folder-mode-row");
  const label = await row.locator("label").boundingBox();
  const toggle = await row.getByRole("switch").boundingBox();
  expect(Math.abs(label!.y + label!.height / 2 - toggle!.y - toggle!.height / 2)).toBeLessThan(12);
  const buttons = page.locator(".share-footer-actions .el-button");
  await buttons.first().scrollIntoViewIfNeeded();
  const close = await buttons.first().boundingBox();
  const submit = await buttons.last().boundingBox();
  expect(submit!.x - close!.x - close!.width).toBeGreaterThanOrEqual(8);
  await page.getByRole("button", { name: "本批转存目标夸克文件夹" }).click();
  const browser = page.getByRole("dialog", { name: "选择夸克文件夹", exact: true });
  await browser.getByRole("button", { name: "共享资料", exact: true }).click();
  await expect(browser).toContainText("此文件夹没有写入权限");
  await expect(browser.getByRole("button", { name: "使用此文件夹" })).toBeDisabled();
  await browser.getByRole("button", { name: "我的云盘", exact: true }).click();
  await expect(browser.getByRole("button", { name: "使用此文件夹" })).toBeEnabled();
});
