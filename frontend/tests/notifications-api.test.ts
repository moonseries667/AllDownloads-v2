import { beforeEach, expect, it, vi } from "vitest";
const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../src/api/client", () => ({ api: { get } }));
import { readAttentionIssues } from "../src/api/notifications";
const issue = { id: "1", taskId: null, objectId: null, code: "FAILED", message: "处理失败", severity: "ERROR", occurredAt: "2026-10-15T09:00:00Z", updatedAt: "2026-10-15T09:00:00Z", read: true, resolved: false };
beforeEach(() => get.mockReset());
it("loads every problem page without acknowledging or resolving anything", async () => {
  get.mockResolvedValueOnce({ items: [issue], nextCursor: "next/2" })
    .mockResolvedValueOnce({ items: [{ ...issue, id: "2" }], nextCursor: null });
  expect(await readAttentionIssues()).toHaveLength(2);
  expect(get.mock.calls).toEqual([["/issues"], ["/issues?cursor=next%2F2"]]);
});
it("rejects incomplete paging instead of replacing existing problems with a partial list", async () => {
  get.mockResolvedValueOnce({ items: [issue], nextCursor: "next" }).mockRejectedValueOnce(new Error("连接中断"));
  await expect(readAttentionIssues()).rejects.toThrow("连接中断");
});
it("detects repeated pagination cursors and incompatible problem data", async () => {
  get.mockResolvedValue({ items: [issue], nextCursor: "repeat" });
  await expect(readAttentionIssues()).rejects.toThrow("分页未完成");
  get.mockResolvedValue({ items: [{ id: "1" }], nextCursor: null });
  await expect(readAttentionIssues()).rejects.toThrow("接口契约");
});
