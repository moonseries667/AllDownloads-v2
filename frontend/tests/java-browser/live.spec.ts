import { expect, test } from "@playwright/test";
import { readdir, readFile } from "node:fs/promises";
import { resolve } from "node:path";
import type { AuthSession } from "../../src/api/accounts";

test.describe.configure({ mode: "serial" });
const pageErrors: string[] = [];
test.beforeEach(async ({ page }) => {
  pageErrors.length = 0;
  page.on("pageerror", error => pageErrors.push(error.message));
});
test.afterEach(async () => { expect(pageErrors).toEqual([]); });

test("production bundle and all empty pages use real Java facts", async ({ page, request }, info) => {
  const assets = resolve("dist/assets");
  const scripts = (await readdir(assets)).filter(file => file.endsWith(".js"));
  const compiled = (await Promise.all(scripts.map(file => readFile(resolve(assets, file), "utf8")))).join("\n");
  expect(compiled).not.toContain("alldownloads.demo.feedback2.v3");
  expect(scripts.some(file => file.startsWith("demo-"))).toBe(false);
  const overview = await request.get("/api/v1/overview");
  const envelope = await overview.json();
  expect(overview.status()).toBe(200);
  expect(envelope.requestId).toBeTruthy();
  expect(envelope.data.runtime.services).toEqual(expect.arrayContaining([
    expect.objectContaining({ id: "java", state: "ACTIVE" }),
    expect.objectContaining({ id: "go", state: "NOT_CONFIGURED" }),
  ]));
  await info.attach("real-java-overview.json", { body: JSON.stringify(envelope, null, 2), contentType: "application/json" });
  await page.goto("/");
  await expect(page.getByRole("region", { name: "运行摘要" })).toContainText("服务未配置");
  await expect(page.locator(".overview-stats")).toContainText("Bilibili 已下载");
  await page.screenshot({ path: info.outputPath("java-overview.png"), fullPage: true });
  for (const [path, heading, empty] of [
    ["/bilibili", "Bilibili", "暂无匹配内容"],
    ["/telegram", "收藏夹", "没有符合条件的收藏内容"],
    ["/quark/cloud", "夸克云盘", "添加一个分享后"],
    ["/downloads", "下载管理", "当前筛选范围暂无下载项目"],
    ["/archives", "归档管理", "暂无需要上传的完整下载项目"],
    ["/tasks", "任务与问题", "暂无匹配任务"],
    ["/settings", "设置与账号", "统一本机下载根目录"],
  ]) {
    await page.goto(path);
    await expect(page.getByRole("heading", { name: heading, exact: true })).toBeVisible();
    await expect(page.locator("#main-content")).toContainText(empty);
    expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false);
  }
  await expect(page.getByText("前端验收场景", { exact: true })).toHaveCount(0);
  expect(await page.evaluate(() => localStorage.getItem("alldownloads.demo.feedback2.v3"))).toBeNull();
  await page.screenshot({ path: info.outputPath("java-settings.png"), fullPage: true });
});

test("source creation, persistent edits and null-progress task cancellation use Java", async ({ page, request }, info) => {
  await page.goto("/bilibili");
  await page.getByRole("button", { name: "添加来源", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "添加 B 站信息来源" });
  await dialog.getByRole("textbox").fill("https://www.bilibili.com/video/BV1testLiveJava\nhttps://invalid.example/video");
  const response = page.waitForResponse(result => result.url().endsWith("/api/v1/bilibili/sources") && result.request().method() === "POST");
  await dialog.getByRole("button", { name: "加入信息来源", exact: true }).click();
  const created = await (await response).json();
  expect(created.data.items).toHaveLength(2);
  expect(created.data.items[0].taskId).toBeTruthy();
  expect(created.data.items[1].error.code).toBe("INVALID_INPUT");
  await expect(dialog).toContainText("https://invalid.example/video");
  await expect(dialog).toContainText("无法识别");
  await dialog.getByRole("button", { name: "关闭", exact: true }).click();
  const sources = (await (await request.get("/api/v1/bilibili/sources")).json()).data.items;
  expect(sources).toHaveLength(1);
  await page.getByRole("button", { name: /^设置来源 / }).click();
  const manage = page.getByRole("dialog", { name: "管理来源", exact: true });
  await expect(manage.getByRole("button", { name: "移除来源", exact: true })).toBeDisabled();
  await manage.locator(".setting-inline .el-switch").click();
  await expect(manage).toContainText("已置顶");
  await manage.getByRole("textbox").fill("Java 实际来源");
  await manage.getByRole("button", { name: "保存", exact: true }).click();
  await page.reload();
  await expect(page.getByRole("navigation", { name: "Bilibili 来源", exact: true })).toContainText("Java 实际来源");
  const persisted = (await (await request.get("/api/v1/bilibili/sources")).json()).data.items[0];
  expect(persisted).toMatchObject({ alias: "Java 实际来源", pinned: true });
  await page.goto("/tasks");
  const row = page.locator(".el-table__body tr").filter({ hasText: "刷新 Bilibili 来源" });
  await expect(row).toContainText("总量待确认");
  await row.getByRole("button", { name: "刷新 Bilibili 来源", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "任务详情" })).toContainText(created.data.items[0].taskId);
  await page.screenshot({ path: info.outputPath("java-null-progress-task.png"), animations: "disabled" });
  await page.getByRole("dialog", { name: "任务详情" }).getByRole("button", { name: "取消", exact: true }).click();
  await expect.poll(async () => (await (await request.get("/api/v1/tasks/" + created.data.items[0].taskId)).json()).data.status).toBe("CANCELLED");
  const projects = await request.get("/api/v1/download-projects");
  expect((await projects.json()).data.items).toEqual([]);
  expect((await (await request.get("/api/v1/download-orders")).json()).data.items).toEqual([]);
});

test("real unsupported actions and invalid targets retain explicit errors", async ({ page, request }, info) => {
  await page.goto("/settings");
  await expect(page.getByRole("button", { name: "选择统一本机下载根目录", exact: true })).toBeVisible();
  const native = page.getByRole("button", { name: "选择统一本机下载根目录", exact: true });
  const original = await native.innerText();
  await native.click();
  await expect(page.locator(".native-directory-field")).toContainText(/不可用|未提供|未注册|尚未|无法/);
  await expect(native).toHaveText(original);
  const preview = await request.post("/api/v1/download-previews", {
    headers: { "Idempotency-Key": crypto.randomUUID() },
    data: { platform: "BILIBILI", selection: { mode: "EXPLICIT", resource: "BILIBILI_CONTENTS", ids: [] } },
  });
  expect(preview.status()).toBe(422);
  expect((await preview.json()).error.code).toBe("INVALID_STORAGE_ROOT");
  await page.getByRole("button", { name: /搜索标题/ }).click();
  await expect(page.getByRole("dialog", { name: "搜索全部内容" })).toContainText("尚未提供全局搜索");
  await page.keyboard.press("Escape");
  await page.goto("/telegram");
  const reads: string[] = [];
  page.on("request", request => reads.push(request.url()));
  await page.reload();
  await expect(page.locator(".telegram-empty")).toBeVisible();
  expect(reads.some(url => url.includes("accountId=telegram-main"))).toBe(true);
  expect(reads.some(url => url.includes("saved-sync"))).toBe(false);
  await page.getByRole("button", { name: "下载收藏增量", exact: true }).click();
  const confirmation = page.getByRole("dialog");
  await expect(confirmation).toBeVisible();
  await info.attach("real-invalid-preview.json", { body: await preview.body(), contentType: "application/json" });
});

test("@accounts real Java account reads, explicit checks and revision-controlled login cancellation", async ({ page, request }, info) => {
  const before = (await (await request.get("/api/v1/tasks?pageSize=200")).json()).data.items;
  const response = await request.get("/api/v1/accounts");
  expect(response.status(), "This profile requires Java with contracts/ACCOUNTS.md implemented").toBe(200);
  const accounts = (await response.json()).data.items;
  expect(accounts.map((account: { id: string }) => account.id).sort()).toEqual(["bilibili-main", "drive-main", "quark-main", "telegram-main"]);
  expect(accounts.every((account: { state: string }) => account.state !== "ACTIVE")).toBe(true);
  // Ordinary queries and page opening must not enqueue identity checks.
  await page.goto("/settings");
  await expect(page.locator(".account-row")).toHaveCount(4);
  const readTasks = (await (await request.get("/api/v1/tasks?pageSize=200")).json()).data.items;
  expect(readTasks.map((task: { id: string }) => task.id)).toEqual(before.map((task: { id: string }) => task.id));
  const row = page.locator(".account-row").filter({ hasText: "Bilibili" });
  const checking = page.waitForResponse(response => response.url().endsWith("/accounts/bilibili-main/check") && response.request().method() === "POST");
  await row.getByRole("button", { name: "检查", exact: true }).click();
  const checkResponse = await checking;
  expect(checkResponse.status()).toBe(202);
  expect((await checkResponse.json()).data).toMatchObject({ accountId: "bilibili-main", taskId: expect.any(String) });
  const beginning = page.waitForResponse(response => response.url().endsWith("/auth-sessions") && response.request().method() === "POST");
  await row.getByRole("button", { name: /登录管理|重新登录/ }).click();
  const session: AuthSession = (await (await beginning).json()).data;
  expect(session).toMatchObject({ accountId: "bilibili-main", platform: "BILIBILI", state: "PENDING", nextAction: "WAIT" });
  const dialog = page.getByRole("dialog", { name: "Bilibili 登录", exact: true });
  await expect(dialog).toContainText("正在等待执行程序");
  await expect(dialog.locator(".auth-qr")).toHaveCount(0);
  await page.screenshot({ path: info.outputPath("real-java-account-login-wait.png"), animations: "disabled" });
  const cancelling = page.waitForResponse(response => response.url().includes("/auth-sessions/") && response.request().method() === "DELETE");
  await dialog.getByRole("button", { name: "取消登录流程", exact: true }).click();
  const cancelled = await cancelling;
  expect(cancelled.status()).toBe(202);
  expect(cancelled.request().headers()["if-match"]).toMatch(/^".+"$/);
  await expect(dialog).toBeHidden();
  const final = (await (await request.get("/api/v1/auth-sessions/" + session.authSessionId)).json()).data;
  expect(final.state).toBe("CANCELLED");
  expect((await (await request.get("/api/v1/download-orders")).json()).data.items).toEqual([]);
  await info.attach("real-java-accounts.json", { body: JSON.stringify(accounts, null, 2), contentType: "application/json" });
});

test("contract stub: QR stays local, polling is bounded to the open flow and cancel uses revision", async ({ page }, info) => {
  const account = { id: "bilibili-main", platform: "BILIBILI", label: "Bilibili 主账号", state: "UNKNOWN", lastCheckedAt: null, reasonCode: null, revision: "1" };
  let session: AuthSession = { authSessionId: "synthetic-auth", accountId: account.id, platform: "BILIBILI", state: "PENDING", nextAction: "SCAN_QR", url: "https://passport.bilibili.com/h5-app/passport/login/scan?fixture=browser-only", message: "合成授权步骤，仅验证界面", expiresAt: new Date(Date.now() + 120000).toISOString(), revision: "1", taskId: null, reasonCode: null };
  let steps = 0, cancelled = false;
  const external: string[] = [];
  page.on("request", request => { if (!request.url().startsWith("http://127.0.0.1:5193") && !request.url().startsWith("data:")) external.push(request.url()); });
  await page.route("**/api/v1/accounts", route => route.fulfill({ json: { data: { items: [account], nextCursor: null }, requestId: "stub-account" } }));
  await page.route("**/api/v1/auth-sessions**", async route => {
    const request = route.request();
    if (request.method() === "DELETE") {
      expect(request.headers()["if-match"]).toBe('"' + session.revision + '"');
      session = { ...session, state: "CANCELLED", nextAction: "NONE", revision: "3" }; cancelled = true;
    } else if (request.method() === "POST") {
      expect(request.headers()["idempotency-key"]).toBeTruthy();
      if (request.url().endsWith("/steps")) { expect(request.postDataJSON()).toEqual({ action: "POLL" }); steps++; }
      else expect(request.postDataJSON()).toEqual({ platform: "BILIBILI" });
    }
    await route.fulfill({ json: { data: session, requestId: "stub-auth" } });
  });
  await page.goto("/settings");
  expect(steps).toBe(0);
  await page.getByRole("button", { name: "登录管理", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Bilibili 登录", exact: true });
  await expect(dialog.getByRole("img", { name: "使用 Bilibili 官方客户端扫码登录", exact: true })).toHaveAttribute("src", /^data:image\/png;base64,/);
  await expect.poll(() => steps).toBeGreaterThan(0);
  await page.screenshot({ path: info.outputPath("contract-stub-local-qr.png"), animations: "disabled" });
  await dialog.getByRole("button", { name: "取消登录流程", exact: true }).click();
  await expect(dialog).toBeHidden();
  expect(cancelled).toBe(true);
  const stoppedAt = steps;
  await page.waitForTimeout(2500);
  expect(steps).toBe(stoppedAt);
  expect(external).toEqual([]);
  expect(await page.evaluate(() => localStorage.getItem("alldownloads.demo.feedback2.v3"))).toBeNull();
});

test("a stopped Java process keeps the page in a visible error state without demo fallback", async ({ page }, info) => {
  await page.goto("/");
  await expect(page.getByRole("region", { name: "运行摘要" })).toContainText("java：运行中");
  const pid = Number(process.env.ALLDOWNLOADS_TEST_JAVA_PID);
  expect(pid).toBeGreaterThan(0);
  process.kill(pid);
  await page.goto("/tasks");
  await expect(page.locator(".connection-banner")).toBeVisible({ timeout: 25000 });
  await expect(page.locator("#main-content")).toContainText("读取失败");
  expect(await page.evaluate(() => localStorage.getItem("alldownloads.demo.feedback2.v3"))).toBeNull();
  await page.screenshot({ path: info.outputPath("java-unavailable.png"), fullPage: true });
});
