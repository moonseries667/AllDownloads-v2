export interface PollingTaskPage {
  items: Array<{ status: string; desiredAction?: string }>;
  nextCursor: string | null;
}

/** An unread next page may contain an active task: keep polling conservatively. */
export function taskPollingDelay(
  page: PollingTaskPage | null,
  hidden: boolean,
) {
  if (hidden) return 15000;
  if (!page || page.nextCursor) return 2000;
  const active = page.items.some(
    (task) =>
      ["QUEUED", "RUNNING", "PAUSING"].includes(task.status) ||
      (task.desiredAction === "CANCEL" &&
        !["SUCCEEDED", "FAILED", "CANCELLED"].includes(task.status)),
  );
  return active ? 2000 : 10000;
}
