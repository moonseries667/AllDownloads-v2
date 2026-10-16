// Consumer helpers for contracts/openapi.yaml, committed baseline d508c7a.
import { api } from "./client";
import type { BilibiliContent, BilibiliSource, DownloadTarget, Page, Preview, Selection, Platform } from "./types";
import { mediaSelectionSession } from "../media-selection";

export type BilibiliSort = "TITLE_ASC" | "TITLE_DESC" | "PUBLISHED_DESC" | "PUBLISHED_ASC";
export function queryString(values: Record<string, string | number | undefined | null>) {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(values)) if (value !== undefined && value !== null && value !== "") params.set(key, String(value));
  return params.size ? "?" + params.toString() : "";
}
export const bilibiliApi = {
  content: (id: string) => api.get<BilibiliContent>("/bilibili/contents/" + encodeURIComponent(id)),
  contents: (filters: Record<string, string | number | undefined> = {}) => api.get<Page<BilibiliContent>>("/bilibili/contents" + queryString(filters)),
  sources: (cursor?: string) => api.get<Page<BilibiliSource>>("/bilibili/sources" + queryString({ cursor, pageSize: 200 })),
  updateSource: (source: BilibiliSource, change: { alias?: string; pinned?: boolean }) =>
    api.patch<BilibiliSource>("/bilibili/sources/" + encodeURIComponent(source.id), change, source.revision),
};

/** Snapshot current saved choices only for the actual selected content range. */
export async function selectionWithSavedMedia(selection: Selection): Promise<Selection> {
  const result: Selection = JSON.parse(JSON.stringify(selection));
  if (result.resource !== "BILIBILI_CONTENTS") return result;
  let ids = result.ids ?? [];
  if (result.mode === "QUERY") {
    ids = [];
    let cursor: string | undefined;
    do {
      const page = await bilibiliApi.contents({ ...result.filters, cursor, pageSize: 200 });
      ids.push(...page.items.map((content) => content.id).filter((id) => !result.excludeIds?.includes(id)));
      cursor = page.nextCursor ?? undefined;
    } while (cursor);
    // Resolve the query once: later refreshes keep this exact range.
    result.mode = "EXPLICIT";
    result.ids = ids;
    delete result.filters;
    delete result.excludeIds;
  }
  const selections = new Map((result.mediaSelections ?? []).map((item) => [item.contentId, item]));
  for (const saved of mediaSelectionSession.snapshotsFor(ids)) selections.set(saved.contentId, {
    contentId: saved.contentId, contentRevision: saved.contentVersion, trackIds: [...saved.mediaIds],
  });
  result.mediaSelections = [...selections.values()].filter((item) => ids.includes(item.contentId));
  if (!result.mediaSelections.length) delete result.mediaSelections;
  return result;
}

export const downloadApi = {
  validateTarget: (path: string) => api.post<{ targetId: string; taskId: string }>("/download-targets/validate", { path }),
  target: (id: string) => api.get<DownloadTarget>("/download-targets/" + encodeURIComponent(id)),
  preview: (platform: Platform, selection: Selection, target?: Pick<DownloadTarget, "id" | "revision">) =>
    api.post<{ previewId: string; taskId: string | null }>("/download-previews", { platform, selection, ...(target ? { target } : {}) }),
  readPreview: (id: string) => api.get<Preview>("/download-previews/" + encodeURIComponent(id)),
  submit: (previewId: string) => api.post<{ orderId: string; taskIds: string[]; reused: boolean }>("/download-orders", { previewId }),
};
