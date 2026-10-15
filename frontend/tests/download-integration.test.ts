import { describe, expect, it } from "vitest";
import { createSeed } from "../src/api/fixtures";
import { createDemoTransport } from "../src/api/demo";
import { createApi } from "../src/api/client";
import { createDownloadManagementApi } from "../src/api/download-management";
function harness() {
  const state = createSeed(), transport = createDemoTransport({ persist: false, state }), client = createApi(transport, () => {});
  return { state, client, service: createDownloadManagementApi(client) };
}
describe("third-part shared mock integration", () => {
  it("new Bilibili batches retain stable video identity and separate instances", async () => {
    const { state, client, service } = harness();
    for (let n = 0; n < 2; n++) {
      const preview = await client.post("/download-previews", { platform: "BILIBILI", selection: { mode: "EXPLICIT", resource: "BILIBILI_CONTENTS", ids: ["b1"] } });
      await client.post("/download-orders", { previewId: preview.previewId });
    }
    const page = await service.list({ platform: "BILIBILI", contentId: "b1" });
    expect(page.items).toHaveLength(2);
    expect(new Set(page.items.map(item => item.batchId)).size).toBe(2);
    expect(new Set(page.items.map(item => item.id)).size).toBe(2);
    const first = (await service.detail(page.items[0]!.id)).parts[0]!;
    await service.cancelPart(page.items[0]!.id, first);
    expect((await service.detail(page.items[0]!.id)).parts[0]!.state).toBe("CANCELLED");
    expect((await service.detail(page.items[1]!.id)).parts[0]!.state).toBe("QUEUED");
    expect(state.management.uploads).toHaveLength(4);
  });
  it("a mixed Telegram message cancels a photo without changing audio or completed outputs", async () => {
    const { state, service } = harness();
    const value = await service.detail("demo-telegram-m1"), photo = value.parts.find(item => item.title.endsWith(".jpg"))!;
    const original = structuredClone(state.management.projects.find(project => project.id === value.id)!);
    await service.cancelPart(value.id, photo);
    const current = await service.detail(value.id);
    expect(current.parts.find(item => item.id === photo.id)).toMatchObject({ state: "RUNNING", desiredAction: "CANCEL" });
    expect(current.parts.find(item => item.title.endsWith(".wav"))).toMatchObject({ state: "RUNNING", desiredAction: "CONTINUE" });
    expect(current.parts[0]!.completedCount).toBe(2);
    expect(current.totalCount).toBe(original.summary.total);
    expect(state.management.projects.find(project => project.id === value.id)!.items.map(item => item.outputs)).toEqual(original.items.map(item => item.outputs));
  });
  it("multi-share batch creates one project per share and local-only orders never enter uploads", async () => {
    const { state, client, service } = harness();
    const target = await client.post("/download-targets/validate", { path: "E:\\fixture-local-only" });
    const preview = await client.post("/download-previews", { platform: "QUARK", selection: { mode: "EXPLICIT", resource: "QUARK_NODES", ids: ["q1-sub1", "q2-file1"] }, target: { id: target.targetId, revision: "1" } });
    const before = structuredClone(state.management.uploads);
    const order = await client.post("/download-orders", { previewId: preview.previewId });
    const projects = (await service.list({ platform: "QUARK" })).items.filter(project => project.batchId === order.orderId);
    expect(projects.map(project => project.contentId).sort()).toEqual(["q1", "q2"]);
    expect(state.management.projects.filter(project => project.batchId === order.orderId).every(project => project.archivePolicy === "NONE" && project.archiveTarget === null)).toBe(true);
    expect(state.management.uploads).toEqual(before);
  });
});
