import { describe, expect, it, vi } from "vitest";
import { createApi } from "../src/api/client";
import { createDownloadManagementApi, detailView, projectView } from "../src/api/download-management";
import { cancelling, downloadHistoryLocation, finished } from "../src/download-management";
import { createDownloadFixture, downloadFixtures } from "./download-fixture";

describe("part three download management contract", () => {
  it("queries an exact content across pages without title or batch identity guesses", async () => {
    const fixture = createDownloadFixture(), service = createDownloadManagementApi(createApi(fixture.transport, () => {}));
    const page = await service.list({ platform: "BILIBILI", contentId: "b1" });
    expect(page.items.map(p => p.id)).toEqual(["bil-new", "bil-old"]);
    expect(new Set(page.items.map(p => p.batchId)).size).toBe(2);
    expect(fixture.requests[0]!.path).toContain("contentId=b1");
    expect(downloadHistoryLocation("id / ?")).toEqual({ path: "/downloads", query: { platform: "BILIBILI", contentId: "id / ?" } });
    expect((await service.list({ contentId: "no-history" })).items).toEqual([]);
    expect(fixture.requests.every(r => r.method === "GET")).toBe(true);
  });
  it("keeps two logical messages in the same scan batch as two top-level projects", () => {
    const messages = downloadFixtures().filter(p => p.platform === "TELEGRAM").map(projectView);
    expect(messages).toHaveLength(2); expect(messages[0]!.batchId).toBe(messages[1]!.batchId);
    expect(messages[0]!.contentId).not.toBe(messages[1]!.contentId);
  });
  it("cancels a mixed album photo with its revision, retaining audio, video and original denominator", async () => {
    const fixture = createDownloadFixture(), service = createDownloadManagementApi(createApi(fixture.transport, () => {}));
    const before = await service.detail("tg-mixed");
    await service.cancelPart(before.id, before.parts.find(i => i.id === "tg-photo")!);
    const accepted = await service.detail("tg-mixed");
    expect(cancelling(accepted.parts[2]!)).toBe(true);
    expect(accepted.parts.slice(0, 2).every(i => i.allowedActions.includes("CANCEL"))).toBe(true);
    expect(accepted.parts[2]!.files).toHaveLength(2);
    fixture.finish("tg-mixed", "tg-photo", "CANCELLED"); fixture.finish("tg-mixed", "tg-video", "SUCCEEDED");
    const after = await service.detail("tg-mixed");
    expect(after.totalCount).toBe(3); expect(after.completedCount).toBe(1); expect(after.cancelledCount).toBe(1);
    expect(after.parts[0]!.files.every(f => f.state === "VERIFIED")).toBe(true);
    expect(after.parts[1]!.state).toBe("RUNNING");
    const action = fixture.requests.find(r => r.method === "POST")!;
    expect(action.path).toBe("/download-projects/tg-mixed/items/tg-photo/actions");
    expect(action.headers["If-Match"]).toBe('"1"'); expect(action.headers["Idempotency-Key"]).toBeTruthy();
  });
  it("top-level cancellation uses the instance and does not change other batches or completed files", async () => {
    const fixture = createDownloadFixture(), service = createDownloadManagementApi(createApi(fixture.transport, () => {}));
    await service.cancelProject(await service.detail("bil-new"));
    const after = await service.detail("bil-new");
    expect(cancelling(after)).toBe(true);
    expect(after.parts[1]!.state).toBe("SUCCEEDED");
    expect((await service.detail("bil-old")).desiredAction).toBe("CONTINUE");
    expect(after.parts[0]!.cancelImpact).toContain("合并");
    fixture.finish("bil-new", "p1", "CANCELLED");
    const terminal = await service.detail("bil-new");
    expect(terminal.state).toBe("PARTIAL"); expect(finished(terminal)).toBe(true); expect(cancelling(terminal)).toBe(false);
  });
  it("preserves completed facts when completion wins and rejects mismatched detail ownership", async () => {
    const fixture = createDownloadFixture(), service = createDownloadManagementApi(createApi(fixture.transport, () => {}));
    const before = await service.detail("bil-old"); fixture.failNext("race");
    await expect(service.cancelProject(before)).rejects.toThrow("VERSION_CHANGED");
    const result = await service.detail(before.id); expect(result.state).toBe("SUCCEEDED"); expect(cancelling(result)).toBe(false);
    const invalid = downloadFixtures()[0]!; invalid.items[0]!.projectId = "other";
    expect(() => detailView(invalid)).toThrow("不匹配");
  });
  it("escapes opaque instance and item IDs and never substitutes task IDs", async () => {
    const post = vi.fn().mockResolvedValue({}), get = vi.fn();
    const service = createDownloadManagementApi({ post, get });
    const detail = detailView(downloadFixtures()[0]!); detail.parts[0]!.id = "part/a ?";
    await service.cancelPart("instance/a ?", detail.parts[0]!);
    expect(post).toHaveBeenCalledWith("/download-projects/instance%2Fa%20%3F/items/part%2Fa%20%3F/actions", { action: "CANCEL" }, "1");
  });
});
