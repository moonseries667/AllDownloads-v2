import { expect, test } from "@playwright/test";
test.beforeEach(async ({ page }) => { await page.goto("/tests/hosts/archive-settings.html"); });
test("native cancellation and unsupported host preserve the previous directory", async ({ page }) => {
  const choose = page.getByRole("button", { name: "选择统一本机下载根目录" });
  await choose.click();
  await expect(page.getByRole("status")).toContainText("已取消选择");
  await expect(choose).toContainText("D:\\fixture-downloads");
  await page.getByLabel("选择结果").selectOption("unavailable");
  await choose.click();
  await expect(page.getByRole("alert")).toContainText("没有可用的交互桌面");
  await expect(choose).toContainText("D:\\fixture-downloads");
});
test("selection updates the draft only and failure retains it", async ({ page }) => {
  await page.getByLabel("选择结果").selectOption("success");
  const choose = page.getByRole("button", { name: "选择统一本机下载根目录" });
  await choose.focus(); await page.keyboard.press("Enter");
  await expect(choose).toContainText("E:\\fixture-selected");
  await expect(page.getByRole("status")).toContainText("保存设置后生效");
  await page.getByLabel("选择结果").selectOption("failure");
  await choose.click();
  await expect(page.getByRole("alert")).toContainText("请求超时");
  await expect(choose).toContainText("E:\\fixture-selected");
  await expect(page.getByTestId("calls")).toHaveText("调用 2 次");
});
test("queue keeps 100 percent in verification and exposes only supported upload retry", async ({ page }) => {
  const verifying = page.locator('[data-upload-id="verify"]');
  await expect(verifying).toContainText("核验中");
  await expect(verifying.locator("progress")).toHaveAttribute("value", "100");
  await expect(verifying.getByRole("button")).toHaveCount(0);
  await page.getByRole("button", { name: "重试上传" }).click();
  await expect(page.getByTestId("actions")).toHaveText("retry");
  await expect(page.getByRole("list", { name: "上传队列" }).getByRole("button", { name: /清理|下载|删除/ })).toHaveCount(0);
  await page.setViewportSize({ width: 1024, height: 576 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});
