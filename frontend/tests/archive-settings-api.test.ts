import { describe, expect, it, vi } from "vitest";
import { archiveRow, createArchiveSettingsApi } from "../src/api/archive-settings";
import type { ArchiveUpload } from "../src/api/management.generated";
const target = { id: "native-target", revision: "target-r1", status: "VALID", path: "E:\\server-only", label: "Server folder", archivePolicy: "NONE", rootId: "host-root", error: null };
const drive = { accountId: "drive-account", folderId: "folder-2", revision: "dir-r2", name: "同名目录", path: "根/同名目录" };
function client(get: (path: string) => unknown, post: (path: string, body: unknown) => unknown = () => ({})) {
  return { get: vi.fn(async (path: string): Promise<any> => get(path)), post: vi.fn(async (path: string, body?: unknown, _revision?: string): Promise<any> => post(path, body)), patch: vi.fn(async (_path: string, _body?: unknown, _revision?: string): Promise<any> => ({})) };
}
describe("committed archive and settings contract", () => {
  it("uses only the qualified upload query, task identity and revision for retry", async () => {
    const upload = { id: "upload-1", projectId: "p1", batchId: "batch", platform: "QUARK", title: "分享", status: "SUCCEEDED", desiredAction: "CONTINUE", allowedActions: ["RETRY", "CANCEL"], revision: "r3", taskId: "upload-task", progress: { doneBytes: "100", totalBytes: "100" }, target: drive, files: [], createdAt: "2026-10-15T00:00:00Z", error: null } as ArchiveUpload;
    const c = client(() => ({ items: [upload], nextCursor: null, total: 1 }));
    const service = createArchiveSettingsApi(c);
    await service.archives({ platform: "QUARK", cursor: "cursor/1" });
    await service.action(upload, "RETRY");
    expect(c.get).toHaveBeenCalledWith("/archives?platform=QUARK&cursor=cursor%2F1&pageSize=50");
    expect(c.post).toHaveBeenCalledWith("/tasks/upload-task/actions", { action: "RETRY" }, "r3");
    expect(archiveRow(upload)).toMatchObject({ state: "VERIFIED", actions: ["RETRY"], destination: "根/同名目录" });
  });
  it("native choice reads a controlled target and never submits a browser path", async () => {
    const c = client(path => path.startsWith("/directory-selections/") ? { status: "SELECTED", targetId: "native-target" } : target, () => ({ selectionId: "select-1" }));
    const result = await createArchiveSettingsApi(c).selectDirectory(new AbortController().signal);
    expect(result).toMatchObject({ status: "SELECTED", path: target.path, target });
    expect(c.post).toHaveBeenCalledExactlyOnceWith("/directory-selections", {});
    expect(c.get).toHaveBeenCalledWith("/download-targets/native-target");
  });
  it("cancel request waits for acknowledgement and suppresses a late selected path", async () => {
    const abort = new AbortController(); abort.abort();
    const c = client(() => ({ status: "SELECTED", targetId: "late-target" }), () => ({ selectionId: "select-2" }));
    expect(await createArchiveSettingsApi(c).selectDirectory(abort.signal)).toEqual({ status: "CANCELLED" });
    expect(c.post).toHaveBeenCalledWith("/directory-selections/select-2/cancel", {});
    expect(c.get.mock.calls).toEqual([["/directory-selections/select-2"]]);
  });
  it("validates stable target ids then saves the exact validation against the original settings revision", async () => {
    let read = 0;
    const c = client(() => ({ id: "validation-1", status: ++read === 1 ? "VALIDATING" : "VALID", revision: "settings-r1" }), () => ({ validationId: "validation-1" }));
    const wait = vi.fn(async () => {});
    const service = createArchiveSettingsApi(c, wait);
    const result = await service.validate(target, drive);
    await service.save(result.id, "settings-r1");
    expect(c.post).toHaveBeenCalledWith("/settings/validate", { downloadTarget: { id: "native-target", revision: "target-r1" }, archiveDestination: { accountId: "drive-account", folderId: "folder-2", revision: "dir-r2" } });
    expect(wait).toHaveBeenCalledTimes(1);
    expect(c.patch).toHaveBeenCalledWith("/settings", { validationId: "validation-1" }, "settings-r1");
  });
  it("cancel during target read cannot overwrite the previous draft", async () => {
    const abort = new AbortController();
    const c = client(path => {
      if (path.startsWith("/directory-selections/")) return { status: "SELECTED", targetId: "native-target" };
      abort.abort(); return target;
    }, () => ({ selectionId: "select-race" }));
    expect(await createArchiveSettingsApi(c).selectDirectory(abort.signal)).toEqual({ status: "CANCELLED" });
  });
  it("pending native selection is bounded and requests cancellation once at timeout", async () => {
    const c = client(() => ({ status: "PENDING" }), () => ({ selectionId: "select-pending" }));
    await expect(createArchiveSettingsApi(c, async () => {}).selectDirectory(new AbortController().signal)).rejects.toThrow("等待超时");
    expect(c.post.mock.calls.filter(call => call[0].endsWith("/cancel"))).toHaveLength(1);
    expect(c.get).toHaveBeenCalledTimes(65);
  });
  it("returns rejected target validation without saving or substituting root", async () => {
    const c = client(() => ({ status: "INVALID", error: { code: "TARGET_NOT_FOUND" } }), () => ({ validationId: "bad-target" }));
    expect(await createArchiveSettingsApi(c).validate(target, drive)).toMatchObject({ status: "INVALID" });
    expect(c.patch).not.toHaveBeenCalled();
  });
});
