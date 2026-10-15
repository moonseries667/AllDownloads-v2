import { computed, shallowRef } from "vue";

// Persistent problem facts follow contracts/FEEDBACK-PART1.md at 6233234.
export interface ServerIssue {
  id: string;
  taskId: string | null;
  objectId: string | null;
  code: string;
  message: string;
  severity: "WARNING" | "ERROR";
  occurredAt: string;
  updatedAt: string;
  read: boolean;
  resolved: boolean;
}
export interface AttentionNotice {
  id: string;
  title: string;
  message: string;
  severity: "error" | "warning" | "info";
  occurredAt: string;
  updatedAt: string;
  href: string;
  source: "server" | "client";
}

export function createNotificationStore() {
  const local = shallowRef(new Map<string, AttentionNotice>());
  const server = shallowRef(new Map<string, AttentionNotice>());
  const attention = computed(() => [...server.value.values(), ...local.value.values()]
    .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt) || a.id.localeCompare(b.id)));

  function recordLocal(notice: Omit<AttentionNotice, "source">) {
    const previous = local.value.get(notice.id);
    local.value = new Map(local.value).set(notice.id, {
      ...notice, source: "client", occurredAt: previous?.occurredAt ?? notice.occurredAt,
    });
  }
  // Replace only after a complete successful query. Read/dismiss is never resolution.
  function replaceServerIssues(issues: ServerIssue[]) {
    server.value = new Map(issues.filter(issue => !issue.resolved).map(issue => [
      "server:" + issue.id,
      {
        id: "server:" + issue.id,
        title: issue.severity === "WARNING" ? "任务需要关注" : "任务未完成",
        message: issue.message,
        severity: issue.severity === "WARNING" ? "warning" : "error",
        occurredAt: issue.occurredAt,
        updatedAt: issue.updatedAt,
        href: issue.taskId ? "/tasks?taskId=" + encodeURIComponent(issue.taskId) : "/tasks",
        source: "server",
      } satisfies AttentionNotice,
    ]));
  }
  return { attention, recordLocal, replaceServerIssues };
}

export const notificationStore = createNotificationStore();
