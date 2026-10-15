import { test, expect, type Page } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/tests/hosts/media.html");
});
async function demoState(page: Page) {
  return page.evaluate(() => JSON.parse(localStorage.getItem("alldownloads.demo.feedback2.v3") || "{}"));
}
test("saves custom media without downloading, then reuses it in the order", async ({ page }) => {
  await page.getByRole("button", { name: "单视频详情", exact: true }).click();
  const drawer = page.getByRole("dialog", { name: "内容详情" });
  await expect(drawer.getByRole("checkbox")).toHaveCount(5);
  await expect(drawer.getByRole("checkbox", { name: "正片：视频", exact: true })).toBeChecked();
  await expect(drawer.getByText("上传日期：2026年9月10日")).toBeVisible();
  await expect(drawer.getByText("收藏夹-影像收藏")).toBeVisible();
  await expect(drawer.getByText("合集-灵感与知识")).toBeVisible();
  await expect(drawer.getByRole("button", { name: "在 B 站打开" })).toHaveCount(0);
  await drawer.getByRole("button", { name: "展开简介" }).click();
  await expect(drawer.getByRole("button", { name: "收起简介" })).toBeVisible();
  await drawer.getByRole("button", { name: "全不选", exact: true }).click();
  await drawer.getByRole("checkbox", { name: "正片：音频", exact: true }).check();
  await drawer.getByRole("button", { name: "保存选定内容" }).click();
  await expect(drawer).not.toBeVisible();
  await expect(page.getByTestId("saved")).toContainText('"mediaIds":["b1-p1-audio"]');
  expect((await demoState(page)).orders).toBeUndefined(); // Saving never calls a mutation transport.
  await page.getByRole("button", { name: "打开待提交清单" }).click();
  const dialog = page.getByRole("dialog", { name: "确认下载清单" });
  await expect(dialog.getByRole("button", { name: "提交下载", exact: true })).toBeEnabled();
  await expect(dialog.getByRole("columnheader", { name: "大小", exact: true })).toHaveCount(0);
  await expect(dialog.getByText("相对目录", { exact: false })).toHaveCount(0);
  await dialog.getByRole("button", { name: "提交下载", exact: true }).click();
  await expect(dialog).not.toBeVisible();
  const state = await demoState(page);
  expect(state.orders[0].items[0].selectedTrackIds).toEqual(["b1-p1-audio"]);
  expect(state.orders[0].items[0].outputs[0].kind).toBe("AUDIO");
});
test("closing a draft preserves the saved choice and explicit empty stays empty", async ({ page }) => {
  await page.getByRole("button", { name: "单视频详情", exact: true }).click();
  let drawer = page.getByRole("dialog", { name: "内容详情" });
  await drawer.getByRole("button", { name: "全不选", exact: true }).click();
  await drawer.getByRole("button", { name: "关闭", exact: true }).click();
  await page.getByRole("button", { name: "单视频详情", exact: true }).click();
  drawer = page.getByRole("dialog", { name: "内容详情" });
  await expect(drawer.getByRole("checkbox", { name: "正片：视频", exact: true })).toBeChecked();
  await drawer.getByRole("button", { name: "全不选", exact: true }).click();
  await drawer.getByRole("button", { name: "保存选定内容" }).click();
  await page.getByRole("button", { name: "打开待提交清单" }).click();
  await expect(page.getByRole("alert")).toContainText("选择为空");
  await expect(page.getByRole("button", { name: "提交下载", exact: true })).toBeDisabled();
});
test("parts and episodes start collapsed; movies use actual language labels", async ({ page }) => {
  for (const [button, first] of [["多 P 详情", "P1"], ["番剧详情", "第 1 集"], ["电影详情", "普通话"]]) {
    await page.getByRole("button", { name: button, exact: true }).click();
    const drawer = page.getByRole("dialog", { name: "内容详情" });
    const toggle = drawer.getByRole("button", { name: new RegExp("^" + first) }).first();
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    await expect(drawer.locator('input[type="checkbox"]:visible')).toHaveCount(0);
    await toggle.focus();
    await page.keyboard.press("Enter");
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    await expect(drawer.locator('input[type="checkbox"]:visible').first()).toBeChecked();
    if (button === "电影详情") {
      await expect(drawer.getByRole("button", { name: /^粤语/ })).toBeVisible();
      await expect(drawer.getByText("作品标识", { exact: true })).toBeVisible();
      await expect(drawer.getByText("BV 号", { exact: true })).toHaveCount(0);
    }
    await drawer.getByRole("button", { name: "关闭", exact: true }).click();
  }
});
test("a validated batch directory invalidates preview, refresh preserves it and submit freezes it", async ({ page }) => {
  await page.getByRole("button", { name: "打开待提交清单" }).click();
  const dialog = page.getByRole("dialog", { name: "确认下载清单" });
  await expect(dialog.getByRole("button", { name: "提交下载", exact: true })).toBeEnabled();
  await dialog.getByRole("button", { name: "修改目录", exact: true }).click();
  await dialog.getByLabel("产品运行主机的本机目录", { exact: true }).fill("E:\\本批测试");
  await dialog.getByRole("button", { name: "校验并使用此目录" }).click();
  await expect(dialog.getByRole("button", { name: "提交下载", exact: true })).toBeDisabled();
  await expect(dialog.getByText("仅本批下载到指定目录，不进入归档；全局默认目录保持不变。")).toBeVisible();
  await dialog.getByRole("button", { name: "刷新", exact: true }).click();
  await expect(dialog.getByRole("button", { name: "提交下载", exact: true })).toBeEnabled();
  await expect(dialog.getByText("仅下载到指定目录，不归档", { exact: true })).toBeVisible();
  await dialog.getByRole("button", { name: "提交下载", exact: true }).click();
  await expect(dialog).not.toBeVisible();
  const state = await demoState(page);
  expect(state.orders[0].target.path).toBe("E:\\本批测试");
  expect(state.orders[0].archivePolicy).toBe("NONE");
  expect(state.settings.downloadRoot).toBe("D:\\AllDownloadsData");
  await page.getByRole("button", { name: "打开待提交清单" }).click();
  await expect(dialog.locator(".target-description")).toContainText("D:\\AllDownloadsData");
});
test("saving an already queued content invalidates the old preview and title sorting is display-only", async ({ page }) => {
  await page.getByRole("button", { name: "打开待提交清单" }).click();
  const dialog = page.getByRole("dialog", { name: "确认下载清单" });
  await expect(dialog.getByRole("button", { name: "提交下载", exact: true })).toBeEnabled();
  await page.evaluate(() => (window as any).mediaTest.save({ contentId: "b1", contentVersion: "demo-v1", mediaIds: ["b1-p1-sub-en"] }));
  await expect(dialog.getByRole("button", { name: "提交下载", exact: true })).toBeDisabled();
  await dialog.getByRole("button", { name: "刷新", exact: true }).click();
  await expect(dialog.getByRole("button", { name: "提交下载", exact: true })).toBeEnabled();
  await expect(dialog.getByRole("columnheader", { name: "大小", exact: true })).toBeVisible();
  await dialog.getByRole("button", { name: "返回选择" }).click();
  await page.getByRole("button", { name: "事实清单", exact: true }).click();
  await expect(dialog.getByText("已归档，无需下载", { exact: true })).toBeVisible();
  await expect(dialog.getByText("已下载，无需下载", { exact: true })).toBeVisible();
  const before = await dialog.locator(".preview-title").allTextContents();
  await dialog.getByRole("button", { name: /^标题，当前/ }).click();
  expect(await dialog.locator(".preview-title").allTextContents()).toEqual([...before].reverse());
});
test("short desktop keeps controls reachable and unknown sizes explicit", async ({ page }, info) => {
  await page.setViewportSize({ width: 1024, height: 576 });
  await page.getByRole("button", { name: "单视频详情", exact: true }).click();
  const drawer = page.getByRole("dialog", { name: "内容详情" });
  await expect(drawer.getByRole("button", { name: "保存选定内容" })).toBeInViewport({ ratio: 1 });
  await drawer.getByRole("checkbox", { name: "正片：弹幕" }).scrollIntoViewIfNeeded();
  await expect(drawer.getByText("大小未知", { exact: true })).toBeVisible();
  await page.screenshot({ path: info.outputPath("detail-short-desktop.png"), animations: "disabled" });
  await drawer.getByRole("button", { name: "关闭", exact: true }).click();
  await expect(drawer).not.toBeVisible();
  await page.getByRole("button", { name: "打开待提交清单" }).click();
  const submit = page.getByRole("button", { name: "提交下载", exact: true });
  await expect(submit).toBeEnabled();
  await expect(page.locator(".dialog-fade-enter-active")).toHaveCount(0);
  await expect(submit).toBeInViewport({ ratio: 1 });
  await page.screenshot({ path: info.outputPath("preview-short-desktop.png"), animations: "disabled" });
});
