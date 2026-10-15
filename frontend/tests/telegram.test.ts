import { describe, expect, it, vi } from "vitest";
import { createSavedMessagesApi, type SavedMessage } from "../src/api/telegram";

const mixed: SavedMessage = {
  id: "group-7", externalId: "31", eligible: true, messageIds: ["31", "32"], groupId: "album-7",
  text: "视频与照片", sentAt: "2026-10-15T09:00:00Z", sourceLinks: [], downloadSummary: { state: "PARTIAL" },
  attachments: [{ id: "v", mediaType: "VIDEO" }, { id: "p", mediaType: "PHOTO" }, { id: "d", mediaType: "DOCUMENT" }],
};
describe("saved-message contract consumer", () => {
  it("preserves the complete backend group, including photos and ordinary files", async () => {
    const get = vi.fn().mockResolvedValue({ items: [mixed], total: 1, nextCursor: "page/2" });
    const api = createSavedMessagesApi({ get });
    const page = await api.list(" 音频 & 视频 ", "first/1");
    expect(page.items).toEqual([mixed]);
    expect(get).toHaveBeenCalledWith("/telegram/messages?q=%E9%9F%B3%E9%A2%91+%26+%E8%A7%86%E9%A2%91&cursor=first%2F1");
  });
  it("does not infer eligibility from file names, MIME or neighboring entries", async () => {
    const get = vi.fn().mockResolvedValue({ items: [{ ...mixed, eligible: false }], total: 1, nextCursor: null });
    await expect(createSavedMessagesApi({ get }).list("")).rejects.toThrow("下载资格");
  });
  it("accepts an empty eligible result and retrieves detail by the stable unit identity", async () => {
    const get = vi.fn().mockResolvedValueOnce({ items: [], total: 0, nextCursor: null }).mockResolvedValueOnce(mixed);
    const api = createSavedMessagesApi({ get });
    expect((await api.list("")).items).toEqual([]);
    expect(await api.detail("group/7")).toEqual(mixed);
    expect(get).toHaveBeenLastCalledWith("/telegram/messages/group%2F7");
  });
});
