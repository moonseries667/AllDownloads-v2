import { describe, expect, it } from "vitest";
import { createSeed } from "../src/api/fixtures";
import { createDemoTransport } from "../src/api/demo";
import { createApi } from "../src/api/client";
import { createArchiveSettingsApi } from "../src/api/archive-settings";
import { driveFolderFixtures, driveTarget } from "../src/api/demo-archive-settings";
describe("archive settings isolated transport", () => {
  it("upload query is distinct from incomplete artifacts and retry never creates a download", async () => {
    const state = createSeed(), transport = createDemoTransport({ persist: false, state });
    const service = createArchiveSettingsApi(createApi(transport, () => {}));
    const uploads = await service.archives();
    expect(state.artifacts.some(item => item.localState === "PARTIAL")).toBe(true);
    expect(uploads.items.some(item => item.title === "周末山间录音与拍摄笔记")).toBe(false);
    const before = structuredClone(state.orders);
    await service.action(uploads.items[0]!, "RETRY");
    expect(state.orders).toEqual(before);
    expect((await service.archives()).items[0]!.status).toBe("QUEUED");
  });
  it("global save keeps queued upload destinations and existing orders frozen", async () => {
    const state = createSeed(), transport = createDemoTransport({ persist: false, state });
    const client = createApi(transport, () => {}), service = createArchiveSettingsApi(client);
    const settings = await client.get("/settings");
    const frozenUploads = structuredClone(state.management.uploads), frozenOrders = structuredClone(state.orders);
    const nativeTarget = { ...settings.defaultTarget, id: "native-selected", path: "E:\\fixture-new-root", rootId: "fixture-new-root", archivePolicy: "NONE" as const };
    state.targets[nativeTarget.id] = nativeTarget;
    const result = await service.validate(nativeTarget, driveTarget(driveFolderFixtures[4]!));
    expect(state.settings.downloadRoot).toBe(settings.downloadRoot);
    const updated = await service.save(result.id, settings.revision);
    expect(updated.downloadRoot).toBe(nativeTarget.path);
    expect(updated.archiveDestination?.folderId).toBe("drive-courses-b");
    expect(state.management.uploads).toEqual(frozenUploads);
    expect(state.orders).toEqual(frozenOrders);
    await expect(service.save(result.id, settings.revision)).rejects.toMatchObject({ code: "VERSION_CHANGED" });
  });
  it("invalid or cross-account Drive identity rejects validation without changing settings", async () => {
    const state = createSeed(), transport = createDemoTransport({ persist: false, state });
    const client = createApi(transport, () => {}), service = createArchiveSettingsApi(client), settings = await client.get("/settings");
    for (const chosen of [driveTarget(driveFolderFixtures[5]!), { ...driveTarget(driveFolderFixtures[1]!), accountId: "q-main" }, { ...driveTarget(driveFolderFixtures[1]!), revision: "stale" }]) {
      expect((await service.validate(settings.defaultTarget, chosen)).status).toBe("INVALID");
    }
    expect((await client.get("/settings")).revision).toBe(settings.revision);
    expect(await service.selectDirectory(new AbortController().signal)).toMatchObject({ status: "UNAVAILABLE" });
  });
});
