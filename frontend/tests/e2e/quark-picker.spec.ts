import { test, expect } from "@playwright/test";

for (const mode of ["error", "loading", "needs-action"]) {
  test(`Quark folder picker recovers ${mode} without accepting a stale target`, async ({ page }) => {
    await page.goto("/tests/fixtures/quark.html?mode=" + mode);
    await page.getByRole("button", { name: "测试转存目标" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByRole("button", { name: "使用此文件夹" })).toBeDisabled();
    await expect(page.getByLabel("已选目标")).toHaveText("null");
    if (mode === "error") await expect(dialog).toContainText("测试目录暂时无法读取");
    if (mode === "loading") await expect(dialog).toContainText("正在获取云盘目录");
    if (mode === "needs-action") await expect(dialog).toContainText("目标已移动");
    await dialog.getByRole("button", { name: "刷新目录" }).click();
    await expect(dialog.getByRole("button", { name: "使用此文件夹" })).toBeEnabled();
    await dialog.getByRole("button", { name: "使用此文件夹" }).click();
    await expect(page.getByLabel("已选目标")).toContainText('"id":"0"');
  });
}

test("Quark folder picker appends pages and supports keyboard cancel without committing", async ({ page }) => {
  await page.goto("/tests/fixtures/quark.html?mode=pages");
  await page.getByRole("button", { name: "测试转存目标" }).focus();
  await page.keyboard.press("Enter");
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("list").getByRole("button")).toHaveText(["学习资料"]);
  await dialog.getByRole("button", { name: "加载更多文件夹" }).click();
  await expect(dialog.getByRole("list").getByRole("button")).toHaveText(["学习资料", "收件箱"]);
  await dialog.getByRole("button", { name: "学习资料", exact: true }).click();
  await page.keyboard.press("Escape");
  await expect(dialog).not.toBeVisible();
  await expect(page.getByLabel("已选目标")).toHaveText("null");
  await expect(page.getByRole("button", { name: "测试转存目标" })).toBeFocused();
});

test("Quark folder picker ignores a response after the dialog closes", async ({ page }) => {
  await page.goto("/tests/fixtures/quark.html?mode=slow");
  await page.getByRole("button", { name: "测试转存目标" }).click();
  await expect(page.getByRole("dialog")).toContainText("正在读取文件夹");
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).not.toBeVisible();
  await expect(page.getByLabel("已选目标")).toHaveText("null");
  await page.getByRole("button", { name: "测试转存目标" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "学习资料", exact: true }).click();
  await expect(page.locator(".folder-current")).toHaveText("当前文件夹：学习资料");
  await page.waitForTimeout(850);
  await expect(page.locator(".folder-current")).toHaveText("当前文件夹：学习资料");
});
