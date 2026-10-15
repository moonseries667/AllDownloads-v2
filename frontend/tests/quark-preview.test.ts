import { describe, expect, it } from "vitest";
import { createSeed } from "../src/api/fixtures";
import { createDemoTransport } from "../src/api/demo";
import { hasReliableSizes, previewFact } from "../src/preview-presentation";

function harness() {
  const state = createSeed();
  const transport = createDemoTransport({ state, persist: false });
  const preview = async (ids: string[], target?: { id: string; revision: string }) => {
    const receipt = await transport.request("POST", "/download-previews", { platform: "QUARK", selection: { mode: "EXPLICIT", resource: "QUARK_NODES", ids }, ...(target ? { target } : {}) });
    return transport.request("GET", "/download-previews/" + receipt.previewId);
  };
  return { state, transport, preview };
}
describe("Quark shared download preview", () => {
  it("freezes folder descendants and preserves true sizes without changing cloud targets", async () => {
    const h = harness();
    const cloudTargets = structuredClone(h.state.shares);
    const pv = await h.preview(["q1-folder"]);
    expect(pv.items.map((item: any) => item.nodeId)).toEqual(["q1-sub1", "q1-sub2"]);
    expect(hasReliableSizes(pv.items)).toBe(true);
    h.state.nodes.push({ ...h.state.nodes.find((node) => node.id === "q1-sub1")!, id: "added-after-preview" });
    const receipt = await h.transport.request("POST", "/download-orders", { previewId: pv.id });
    const order = await h.transport.request("GET", "/download-orders/" + receipt.orderId);
    expect(order.items.map((item: any) => item.nodeId)).toEqual(["q1-sub1", "q1-sub2"]);
    expect(h.state.shares).toEqual(cloudTargets);
  });
  it("does not reuse files from the default directory for a custom batch target", async () => {
    const h = harness();
    const original = h.state.settings.downloadRoot;
    const existing = await h.preview(["q1-file1"]);
    expect(previewFact(existing.items[0])).toBe("已下载");
    const validation = await h.transport.request("POST", "/download-targets/validate", { path: "D:\\IsolatedBatch" });
    const target = await h.transport.request("GET", "/download-targets/" + validation.targetId);
    const pv = await h.preview(["q1-file1"], target);
    expect(pv.archivePolicy).toBe("NONE");
    expect(pv.items[0]).toMatchObject({ decision: "DOWNLOAD", reasonCode: "CUSTOM_TARGET_NO_ARCHIVE" });
    const receipt = await h.transport.request("POST", "/download-orders", { previewId: pv.id });
    expect(h.state.orders.find((order) => order.id === receipt.orderId)).toMatchObject({ archivePolicy: "NONE", target: { path: "D:\\IsolatedBatch" } });
    expect(h.state.settings.downloadRoot).toBe(original);
    expect(h.state.tasks.filter((task) => receipt.taskIds.includes(task.id)).map((task) => task.kind)).toEqual(["DOWNLOAD"]);
    target.revision = "2";
    h.state.targets[target.id]!.revision = "2";
    const stale = await h.preview(["q1-sub1"]);
    h.state.nodes.find((node) => node.id === "q1-sub1")!.version = "v2";
    await expect(h.transport.request("POST", "/download-orders", { previewId: stale.id })).rejects.toMatchObject({ code: "VERSION_CHANGED" });
  });
  it("keeps unknown sizes null and distinguishes uncertain state from confirmed deletion", async () => {
    const h = harness();
    const node = h.state.nodes.find((item) => item.id === "q1-sub1")!;
    node.sizeBytes = null;
    node.availability = "UNKNOWN";
    let pv = await h.preview([node.id]);
    expect(hasReliableSizes(pv.items)).toBe(false);
    expect(previewFact(pv.items[0])).toBe("状态待确认");
    node.availability = "REMOVED";
    pv = await h.preview([node.id]);
    expect(previewFact(pv.items[0])).toBe("源文件已删除");
    expect(pv.summary.blocked).toBe(1);
  });
  it("rejects stale or missing cloud target references without creating transfer tasks", async () => {
    const h = harness();
    const before = h.state.tasks.length;
    const result = await h.transport.request("POST", "/quark/shares", { accountId: "q-main", entries: [
      { url: "https://pan.quark.cn/s/TestWrongVersion", targetFolderId: "folder-learning", targetFolderRevision: "old" },
      { url: "https://pan.quark.cn/s/TestMissingFolder", targetFolderId: "missing", targetFolderRevision: "1" },
    ] });
    expect(result.items.map((item: any) => item.error.code)).toEqual(["VERSION_CHANGED", "NOT_FOUND"]);
    expect(h.state.tasks).toHaveLength(before);
  });
});
