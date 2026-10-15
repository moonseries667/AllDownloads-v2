import { describe, expect, it } from "vitest";
import { createSeed } from "../src/api/fixtures";
import { createDemoTransport } from "../src/api/demo";
import { createSavedMessagesApi } from "../src/api/telegram";

describe("second-part shared demo integration", () => {
  it("returns only Java-eligible logical units with every mixed-group attachment", async () => {
    const state = createSeed();
    const transport = createDemoTransport({ state, persist: false });
    const saved = createSavedMessagesApi({ get: (path) => transport.request("GET", path) });
    const page = await saved.list("");
    expect(page.total).toBe(2);
    expect(page.items.map((message) => message.id)).toEqual(["m1", "m3"]);
    const mixed = await saved.detail("m1");
    expect(mixed.messageIds).toEqual(["1001", "1004"]);
    expect(mixed.attachments.map((item) => item.mediaType)).toEqual(["video/mp4", "audio/wav", "PHOTO", "DOCUMENT"]);
    for (const id of ["m2", "m-photo-only", "m-document-only", "m-photo-document"])
      await expect(saved.detail(id)).rejects.toMatchObject({ code: "NOT_FOUND" });
    const empty = await saved.list("仅照片示例");
    expect(empty.items).toEqual([]);
    expect(empty.total).toBe(0);
  });
  it("does not allow the removed link or manual attachment preview to enqueue work", async () => {
    const transport = createDemoTransport({ persist: false });
    const before = transport.inspect();
    await expect(transport.request("POST", "/telegram/link-downloads", { links: ["https://t.me/test/1"] })).rejects.toMatchObject({ code: "FEATURE_REMOVED" });
    await expect(transport.request("POST", "/download-previews", { platform: "TELEGRAM", selection: { mode: "EXPLICIT", resource: "TELEGRAM_ATTACHMENTS", ids: ["att1"] } })).rejects.toMatchObject({ code: "FEATURE_REMOVED" });
    expect(transport.inspect().tasks).toEqual(before.tasks);
    expect(transport.inspect().orders).toEqual(before.orders);
  });
  it("keeps stable unresolved issues through repeated reads, acknowledgement and retry", async () => {
    const state = createSeed();
    const transport = createDemoTransport({ state, persist: false });
    const first = await transport.request("GET", "/issues");
    expect(first.items.length).toBeGreaterThan(0);
    expect(await transport.request("GET", "/issues")).toEqual(first);
    const issue = first.items[0];
    const task = state.tasks.find((item) => item.id === issue.taskId)!;
    const status = task.status;
    await transport.request("POST", "/issues/" + encodeURIComponent(issue.id) + "/acknowledge");
    let current = await transport.request("GET", "/issues");
    expect(current.items.find((item: any) => item.id === issue.id)).toMatchObject({ read: true, resolved: false, occurredAt: issue.occurredAt });
    expect(task.status).toBe(status);
    if (task.allowedActions.includes("RETRY")) await transport.request("POST", "/tasks/" + task.id + "/actions", { action: "RETRY" });
    current = await transport.request("GET", "/issues");
    expect(current.items.find((item: any) => item.id === issue.id)).toMatchObject({ read: true, resolved: false });
    const restored = createDemoTransport({ state: transport.inspect(), persist: false });
    expect((await restored.request("GET", "/issues")).items).toEqual(current.items);
  });
});
