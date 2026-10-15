import { describe, expect, it, vi } from "vitest";
import { folderPath, loadQuarkFolders, selectableFolder, type QuarkFolderPage } from "../src/api/quark-folders";
import { demoQuarkFolderPage, validateDemoQuarkTarget } from "../src/api/quark-folder-fixtures";

const page = (parentId?: string) => demoQuarkFolderPage(new URLSearchParams({ accountId: "q-main", ...(parentId ? { parentId } : {}) }));
describe("Quark folder selection contract (6233234)", () => {
  it("reads the actual root and builds root-to-leaf paths without using display paths as identity", () => {
    expect(selectableFolder(page())).toEqual({ id: "0", revision: "1", path: "" });
    const result = page("folder-camera");
    result.parent!.path = "this-display-field-is-not-an-identity";
    expect(folderPath(result)).toBe("学习资料/课程/摄影");
    expect(selectableFolder(result)).toEqual({ id: "folder-camera", revision: "1", path: "学习资料/课程/摄影" });
    expect(result.items).toEqual([]);
  });
  it("does not conflate same-name folders in different branches", () => {
    expect(selectableFolder(page("folder-course"))).toEqual({ id: "folder-course", revision: "1", path: "学习资料/课程" });
    expect(selectableFolder(page("folder-inbox-course"))).toEqual({ id: "folder-inbox-course", revision: "1", path: "收件箱/课程" });
  });
  it("blocks missing, unavailable, read-only and pending folders", () => {
    expect(selectableFolder(page("folder-readonly"))).toBeNull();
    for (const status of ["LOADING", "NEEDS_ACTION"] as const) expect(selectableFolder({ ...page(), status })).toBeNull();
    expect(selectableFolder({ ...page(), parent: null })).toBeNull();
    expect(() => page("removed")).toThrow("已不存在");
    const result = page();
    expect(selectableFolder({ ...result, parent: { ...result.parent!, availability: "REMOVED" } })).toBeNull();
  });
  it("sends the account, stable parent ID and opaque cursor to Java", async () => {
    const request = vi.fn().mockResolvedValue(page());
    await loadQuarkFolders({ request }, "q-main");
    expect(request).toHaveBeenLastCalledWith("GET", "/quark/folders?accountId=q-main");
    await loadQuarkFolders({ request }, "q-main", "node/1", "page+2");
    expect(request).toHaveBeenLastCalledWith("GET", "/quark/folders?accountId=q-main&parentId=node%2F1&cursor=page%2B2");
  });
  it("requires exact target revision and write permission before accepting transfer", () => {
    expect(validateDemoQuarkTarget("0", "1").id).toBe("0");
    expect(() => validateDemoQuarkTarget("folder-course", "old")).toThrow("已变化");
    expect(() => validateDemoQuarkTarget("folder-readonly", "1")).toThrow("没有写入权限");
    expect(() => validateDemoQuarkTarget("missing", "1")).toThrow("不存在");
  });
  it("can paginate folders without adding prefixes to their names", () => {
    const result: QuarkFolderPage = demoQuarkFolderPage(new URLSearchParams({ accountId: "q-main", pageSize: "1" }));
    expect(result.items.map((item) => item.name)).toEqual(["学习资料"]);
    expect(result.nextCursor).toBe("1");
    const next = demoQuarkFolderPage(new URLSearchParams({ accountId: "q-main", pageSize: "1", cursor: result.nextCursor! }));
    expect(next.items.map((item) => item.name)).toEqual(["收件箱"]);
  });
});
