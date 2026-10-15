import { h } from "vue";
import { ElNotification } from "element-plus";
import { notificationStore } from "./store";

type NoticeType = "success" | "error" | "warning" | "info";
interface NoticeInput {
  title: string;
  message: string;
  type: NoticeType;
  key?: string;
  href?: string;
  action?: { label: string; run: () => void };
}
const lastShown = new Map<string, number>();

export function notify(input: NoticeInput) {
  const href = input.href ?? (typeof window === "undefined" ? "/" : window.location.pathname);
  const key = input.key ?? JSON.stringify([input.type, href, input.title, input.message]);
  const now = Date.now();
  if (input.type !== "success") {
    const timestamp = new Date(now).toISOString();
    notificationStore.recordLocal({
      id: "client:" + key, title: input.title, message: input.message,
      severity: input.type, occurredAt: timestamp, updatedAt: timestamp, href,
    });
    if (now - (lastShown.get(key) ?? 0) < 60000) return;
    for (const [oldKey, time] of lastShown) if (now - time >= 60000) lastShown.delete(oldKey);
    lastShown.set(key, now);
  }
  const close = () => handle.close();
  const handle = ElNotification({
    title: input.title, type: input.type, position: "top-right",
    duration: input.type === "success" ? 3000 : 6000,
    onClick: close,
    message: h("div", [
      h("button", {
        class: "notice-dismiss-body", type: "button",
        "aria-label": "关闭提示：" + input.message,
        onClick: (event: MouseEvent) => { event.stopPropagation(); close(); },
      }, input.message),
      ...(input.action ? [h("button", {
        class: "notice-action", type: "button",
        onClick: (event: MouseEvent) => { event.stopPropagation(); input.action?.run(); },
      }, input.action.label)] : []),
    ]),
  });
  return handle;
}

export function notifyError(error: unknown) {
  const message = error instanceof Error ? error.message.split("\n")[0] : "请求失败，请重试";
  return notify({ title: "操作未完成", message, type: "error" });
}
export function notifySuccess(message: string, title = "已受理") {
  return notify({ title, message, type: "success" });
}
export function notifyWarning(message: string) {
  return notify({ title: "需要关注", message, type: "warning" });
}
