import { api } from "./client";
import type { ServerIssue } from "../notifications/store";

export async function readAttentionIssues(): Promise<ServerIssue[]> {
  const issues: ServerIssue[] = [];
  const seen = new Set<string>();
  let cursor: string | null = null;
  do {
    const page: { items: ServerIssue[]; nextCursor: string | null } = await api.get(
      "/issues" + (cursor ? "?cursor=" + encodeURIComponent(cursor) : ""),
    );
    for (const issue of page.items) {
      if (typeof issue.id !== "string" || typeof issue.message !== "string" ||
          typeof issue.occurredAt !== "string" || typeof issue.updatedAt !== "string" ||
          typeof issue.resolved !== "boolean" || !["ERROR", "WARNING"].includes(issue.severity)) {
        throw new Error("问题列表格式与接口契约不一致，请刷新后重试");
      }
      issues.push(issue);
    }
    cursor = page.nextCursor;
    if (cursor && seen.has(cursor)) throw new Error("问题列表分页未完成，请重新读取");
    if (cursor) seen.add(cursor);
  } while (cursor);
  return issues;
}
