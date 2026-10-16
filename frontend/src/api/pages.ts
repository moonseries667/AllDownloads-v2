import { api } from "./client";
import type { Page } from "./types";

/** Read a complete collection before deriving totals or applying local filters. */
export async function readAllPages<T>(path: string): Promise<T[]> {
  const items: T[] = [], seen = new Set<string>();
  let cursor: string | null = null;
  do {
    const query = new URLSearchParams({ pageSize: "200" });
    if (cursor) query.set("cursor", cursor);
    const page: Page<T> = await api.get(path + (path.includes("?") ? "&" : "?") + query);
    items.push(...page.items);
    cursor = page.nextCursor;
    if (cursor && seen.has(cursor)) throw Error("列表分页游标重复，请重新读取。");
    if (cursor) seen.add(cursor);
  } while (cursor);
  return items;
}
