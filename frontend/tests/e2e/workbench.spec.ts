import { test, expect } from "@playwright/test";
test("all desktop routes render without runtime errors or horizontal overflow", async ({
  page,
}) => {
  test.setTimeout(120000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  for (const [width, height] of [
    [1280, 720],
    [1440, 900],
    [1920, 1080],
    [1024, 576],
    [1152, 720],
    [1536, 864],
  ]) {
    await page.setViewportSize({ width, height });
    for (const path of [
      "/",
      "/bilibili",
      "/telegram",
      "/quark/cloud",
      "/downloads",
      "/archives",
      "/tasks",
      "/settings",
    ]) {
      await page.goto(path);
      await expect(page.locator(".page-title")).toBeVisible();
      await expect
        .poll(() =>
          page.evaluate(
            () => document.documentElement.scrollWidth <= window.innerWidth + 1,
          ),
        )
        .toBe(true);
    }
  }
  expect(errors).toEqual([]);
});
test("Bilibili cross-page selection stays fixed until preview and explicit order submission", async ({ page }) => {
  await page.goto("/bilibili");
  const firstTitle = await page.locator(".video-title").first().innerText();
  await page.locator(".cover-checkbox").first().click();
  await page.locator(".el-pager .number").filter({ hasText: /^2$/ }).click();
  const secondTitle = await page.locator(".video-title").first().innerText();
  await page.locator(".cover-checkbox").first().click();
  await expect(page.locator(".selection-bar")).toContainText("已选择 2 项");
  await page.getByRole("button", { name: "预览下载清单", exact: true }).click();
  await expect(page.getByRole("dialog")).toContainText(firstTitle);
  await expect(page.getByRole("dialog")).toContainText(secondTitle);
  const state = await page.evaluate(() => JSON.parse(localStorage.getItem("alldownloads.demo.feedback2.v3")!));
  expect(state.orders).toHaveLength(1);
  const preview = Object.values(state.previews)[0] as any;
  expect(new Set(preview.items.map((item: any) => item.contentId)).size).toBe(2);
  await page.getByRole("button", { name: "提交下载", exact: true }).click();
  await expect(page.getByRole("dialog")).toBeHidden();
  await page.goto("/downloads");
  await expect(page.locator(".download-table tbody tr[data-project-id]")).toHaveCount(state.management.projects.length + 2);
  await expect(page.locator(".download-table tbody")).toContainText("等待中");
});
test("Bilibili filtered all-selection supports exclusions and retains scope after filters change", async ({ page }) => {
  await page.goto("/bilibili");
  const excludedTitle = await page.locator(".video-title").first().innerText();
  await page.getByRole("button", { name: "全选筛选结果（12）" }).click();
  await page.locator(".cover-checkbox").first().click();
  await page.getByRole("button", { name: "多 P 视频", exact: true }).click();
  await expect(page.locator(".selection-bar")).toContainText("已选择 11 项");
  await page.getByRole("button", { name: "预览下载清单", exact: true }).click();
  await expect(page.getByRole("dialog")).toContainText("无访问权限");
  await expect(page.getByRole("dialog")).not.toContainText(excludedTitle);
  const state = await page.evaluate(() => JSON.parse(localStorage.getItem("alldownloads.demo.feedback2.v3")!));
  const preview = Object.values(state.previews)[0] as any;
  expect(new Set(preview.items.map((item: any) => item.contentId)).size).toBe(11);
});
test("Quark folder selection expands only on preview and creates no order when cancelled", async ({
  page,
}) => {
  await page.goto("/quark/cloud");
  await expect(page.locator(".el-table__body")).toContainText("摄影课程");
  const row = page
    .locator(".el-table__body tbody tr")
    .filter({ hasText: "摄影课程" });
  await row.locator(".el-checkbox").click();
  await expect(row.getByRole("checkbox")).toBeChecked();
  await page.getByRole("button", { name: "预览下载", exact: true }).click();
  await expect(page.getByRole("dialog")).toContainText(
    "01 光线的方向.mp4",
  );
  await expect(
    page.getByRole("dialog").locator(".el-table__body tbody tr"),
  ).toHaveCount(2);
  await page.getByRole("button", { name: "返回选择", exact: true }).click();
  const state = await page.evaluate(() =>
    JSON.parse(localStorage.getItem("alldownloads.demo.feedback2.v3")!),
  );
  expect(state.orders).toHaveLength(1);
});
test("Quark share duplicate is reused; transfer is distinct from download", async ({
  page,
}) => {
  await page.goto("/quark/cloud");
  await page.getByRole("button", { name: "添加分享转存", exact: true }).click();
  await page
    .getByRole("textbox", { name: "夸克分享链接，每行一条" })
    .fill("https://pan.quark.cn/s/abcd12345678");
  await page.getByRole("button", { name: "本批转存目标夸克文件夹" }).click();
  const picker = page.getByRole("dialog", { name: "选择夸克文件夹", exact: true });
  await picker.getByRole("button", { name: "学习资料", exact: true }).click();
  await picker.getByRole("button", { name: "使用此文件夹", exact: true }).click();
  await page
    .getByRole("button", { name: "转存并维护更新", exact: true })
    .click();
  await expect(page.getByRole("dialog")).toContainText("已复用已有分享");
  const state = await page.evaluate(() =>
    JSON.parse(localStorage.getItem("alldownloads.demo.feedback2.v3")!),
  );
  expect(state.orders).toHaveLength(1);
  expect(state.shares).toHaveLength(2);
});
test("Telegram requires initial scan confirmation and expands metadata with Shanghai time", async ({
  page,
}) => {
  await page.goto("/telegram");
  await expect(
    page.getByRole("switch", { name: "启用收藏监控" }),
  ).toBeDisabled();
  await page
    .getByRole("button", { name: "展开消息正文和附件", exact: true })
    .first()
    .click();
  await expect(page.getByLabel("完整消息正文").first()).toContainText(
    "虚构演示数据",
  );
  await expect(page.locator(".attachment-list")).toContainText(
    "树林环境声.wav",
  );
  await expect(page.locator(".message-time").first()).toContainText(
    "2026.10.12-09.15.30",
  );
  await page.getByRole("button", { name: "下载收藏增量", exact: true }).click();
  await expect(page.getByRole("dialog")).toContainText("消息 ID 1");
  await page
    .getByRole("button", { name: "确认并开始扫描", exact: true })
    .click();
  await expect(page.locator(".sync-progress")).toContainText("等待中");
  const state = await page.evaluate(() =>
    JSON.parse(localStorage.getItem("alldownloads.demo.feedback2.v3")!),
  );
  expect(
    state.tasks.find(
      (t: any) => t.platform === "TELEGRAM" && t.kind === "INFO_SYNC",
    ).allowedActions,
  ).toEqual(["CANCEL"]);
});
test("Telegram legacy tabs return to saved media with attachment search", async ({ page }) => {
  await page.goto("/telegram?tab=resources");
  await expect(page.getByRole("heading", { name: "收藏夹", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "消息链接下载", exact: true })).toHaveCount(0);
  await page.getByRole("textbox", { name: "搜索消息正文", exact: true }).fill("树林环境声.wav");
  await expect(page.locator(".message-card")).toHaveCount(1);
  await page.getByRole("button", { name: "展开消息正文和附件", exact: true }).click();
  await expect(page.locator(".attachment-row")).toHaveCount(4);
});
test("archive management contains uploads only and retry keeps source downloads unchanged", async ({ page }) => {
  await page.goto("/archives");
  await expect(page.getByRole("heading", { name: "归档管理", exact: true })).toBeVisible();
  await expect(page.getByRole("list", { name: "上传队列" })).toContainText("设计的秩序");
  await expect(page.getByRole("list", { name: "上传队列" })).not.toContainText("周末山间录音与拍摄笔记");
  await expect(page.getByRole("button", { name: /清理|成果详情|打开本机/ })).toHaveCount(0);
  await page.getByRole("button", { name: "重试上传", exact: true }).click();
  await expect(page.locator('[data-upload-id="archive-failed"]')).toContainText("待上传");
  const state = await page.evaluate(() => JSON.parse(localStorage.getItem("alldownloads.demo.feedback2.v3")!));
  expect(state.orders).toHaveLength(1);
  expect(state.artifacts.find((item: any) => item.id === "a1").localState).toBe("VERIFIED");
});
test("disk failure preserves preview selection and does not create a completed order", async ({
  page,
}) => {
  await page.goto("/settings");
  await page.locator(".demo-controls .el-select__wrapper").click();
  await page.getByRole("option", { name: "本机空间不足", exact: true }).click();
  await page.getByRole("button", { name: "加载场景", exact: true }).click();
  await page.goto("/bilibili");
  await page
    .locator(".video-card")
    .first()
    .getByRole("button", { name: "下载", exact: true })
    .click();
  await page.getByRole("button", { name: "提交下载", exact: true }).click();
  await expect(page.getByRole("dialog")).toContainText("本机剩余空间不足");
  const state = await page.evaluate(() =>
    JSON.parse(localStorage.getItem("alldownloads.demo.feedback2.v3")!),
  );
  expect(state.orders).toHaveLength(1);
});
test("global search routes to a platform with its query applied", async ({
  page,
}) => {
  await page.goto("/");
  await page.getByRole("button", { name: /搜索标题、文件名/ }).click();
  await page
    .getByRole("textbox", { name: "搜索全部内容", exact: true })
    .fill("设计参考手册");
  await page.locator(".search-results button").first().click();
  await expect(page).toHaveURL(/quark\/cloud\?q=/);
  await expect(
    page.getByRole("textbox", { name: "搜索当前目录或整个分享" }),
  ).toHaveValue("设计参考手册.pdf");
  await expect(page.locator(".el-table__body tbody tr")).toHaveCount(1);
});
