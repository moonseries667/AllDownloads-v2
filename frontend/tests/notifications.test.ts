import { describe, expect, it, vi } from "vitest";
import { createNotificationStore, notificationStore, type ServerIssue } from "../src/notifications/store";

const { show, close } = vi.hoisted(() => ({ show: vi.fn(), close: vi.fn() }));
vi.mock("element-plus", () => ({ ElNotification: show.mockReturnValue({ close }) }));
import { notify } from "../src/notifications/notify";

const issue: ServerIssue = {
  id: "issue1", taskId: "task/1", objectId: null, code: "AUTH_REQUIRED", message: "请重新登录",
  severity: "ERROR", occurredAt: "2026-10-15T09:00:00Z", updatedAt: "2026-10-15T09:00:00Z", read: false, resolved: false,
};
describe("attention lifecycle", () => {
  it("keeps read issues, deduplicates repeated polls and removes only resolved facts", () => {
    const store = createNotificationStore();
    store.replaceServerIssues([issue, issue]);
    store.replaceServerIssues([{ ...issue, read: true }]);
    expect(store.attention.value).toHaveLength(1);
    expect(store.attention.value[0].href).toBe("/tasks?taskId=task%2F1");
    store.replaceServerIssues([{ ...issue, resolved: true }]);
    expect(store.attention.value).toHaveLength(0);
  });
  it("updates repeated local failures without losing their original occurrence", () => {
    const store = createNotificationStore();
    const local = { id: "client:1", title: "校验失败", message: "目录不可用", severity: "warning" as const, href: "/settings", occurredAt: "2026-10-15T09:00:00Z", updatedAt: "2026-10-15T09:00:00Z" };
    store.recordLocal(local);
    store.recordLocal({ ...local, occurredAt: "2026-10-15T09:01:00Z", updatedAt: "2026-10-15T09:01:00Z" });
    store.replaceServerIssues([issue]);
    store.replaceServerIssues([]);
    expect(store.attention.value).toHaveLength(1);
    expect(store.attention.value[0].occurredAt).toBe(local.occurredAt);
  });
  it("closes only the popup and keeps every non-success severity in attention", () => {
    for (const type of ["error", "warning", "info"] as const) {
      notify({ title: "提示", message: type, type, key: type, href: "/telegram" });
      show.mock.lastCall![0].onClick();
      expect(notificationStore.attention.value.some(n => n.id === "client:" + type)).toBe(true);
    }
    expect(close).toHaveBeenCalledTimes(3);
    const count = notificationStore.attention.value.length;
    notify({ title: "成功", message: "已受理", type: "success" });
    show.mock.lastCall![0].onClick();
    expect(notificationStore.attention.value).toHaveLength(count);
  });
  it("suppresses repeat popup noise while retaining one current failure", () => {
    show.mockClear();
    notify({ title: "失败", message: "重试仍失败", type: "error", key: "repeat", href: "/settings" });
    notify({ title: "失败", message: "重试仍失败", type: "error", key: "repeat", href: "/settings" });
    expect(show).toHaveBeenCalledTimes(1);
    expect(notificationStore.attention.value.filter(n => n.id === "client:repeat")).toHaveLength(1);
  });
});
