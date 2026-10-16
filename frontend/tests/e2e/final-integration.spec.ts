import { expect, test } from "@playwright/test";

test("final integration keeps a custom batch target separate from an existing archive", async ({
  page,
}) => {
  await page.goto("/bilibili");
  await page
    .getByRole("textbox", { name: "搜索视频标题", exact: true })
    .fill("手冲咖啡的日常");
  const card = page.locator(".video-card");
  await expect(card).toHaveCount(1);
  await card.getByRole("button", { name: "下载", exact: true }).click();
  const dialog = page.getByRole("dialog", {
    name: "确认下载清单",
    exact: true,
  });
  await expect(dialog).toContainText("已归档");
  await dialog.getByRole("button", { name: "修改目录", exact: true }).click();
  await dialog
    .getByRole("textbox", { name: "产品运行主机的本机目录", exact: true })
    .fill("E:\\最终验收本批");
  await dialog
    .getByRole("button", { name: "校验并使用此目录", exact: true })
    .click();
  await expect(
    dialog.getByRole("button", { name: "提交下载", exact: true }),
  ).toBeDisabled();
  await dialog.getByRole("button", { name: "刷新", exact: true }).click();
  await expect(
    dialog.getByRole("button", { name: "提交下载", exact: true }),
  ).toBeEnabled();
  await expect(dialog.locator(".el-table__body")).not.toContainText("已归档");
  await expect(dialog.locator(".el-table__body")).toContainText("不归档");
  await dialog.getByRole("button", { name: "提交下载", exact: true }).click();
  await expect(dialog).toBeHidden();
  const state = await page.evaluate(() =>
    JSON.parse(localStorage.getItem("alldownloads.demo.feedback2.v3")!),
  );
  expect(state.orders[0].archivePolicy).toBe("NONE");
  expect(state.orders[0].target.path).toBe("E:\\最终验收本批");
  expect(
    state.orders[0].items.every(
      (item: { archiveState: string; downloadState: string }) =>
        item.archiveState === "NOT_REQUESTED" &&
        item.downloadState === "NOT_REQUESTED",
    ),
  ).toBe(true);
  await card.getByRole("button", { name: "详情", exact: true }).click();
  await page.getByRole("link", { name: "查看下载记录", exact: true }).click();
  await expect(page).toHaveURL(/downloads\?platform=BILIBILI&contentId=b9/);
  await expect(page.locator(".scope-banner")).toContainText("b9");
  await expect(
    page.locator(".download-table tbody tr[data-project-id]"),
  ).toHaveCount(1);
});

test("navigation attention count uses issue facts with the frozen overview shape", async ({
  page,
}) => {
  await page.goto("/");
  const taskLink = page
    .getByRole("navigation", { name: "主导航" })
    .getByRole("link", { name: /任务与问题/ });
  await expect(taskLink.locator("b")).toHaveText("2");
  await page.getByRole("button", { name: /通知/ }).click();
  await expect(
    page
      .locator(".notification-center")
      .getByRole("heading", { name: /需要关注/ }),
  ).toBeVisible();
});
