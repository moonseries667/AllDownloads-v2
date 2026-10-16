import { computed, reactive, readonly, ref } from "vue";

/** Browser-session choices, keyed by stable content identity, never by source/page. */
export interface SavedMediaSelection {
  contentId: string;
  contentVersion: string;
  mediaIds: string[];
}

export interface SelectableMedia {
  id: string;
  kind: "VIDEO" | "AUDIO" | "SUBTITLE" | "DANMAKU";
  availability: string;
  subtitleKind?: "HUMAN" | "AI" | "UNKNOWN" | null;
}

export function isSelectableMedia(media: SelectableMedia) {
  return media.availability === "AVAILABLE" &&
    (media.kind !== "SUBTITLE" || media.subtitleKind === "HUMAN");
}

export function defaultMediaIds(media: readonly SelectableMedia[]) {
  return media.filter(isSelectableMedia).map((item) => item.id);
}

const copy = (value: SavedMediaSelection): SavedMediaSelection => ({
  ...value,
  mediaIds: [...value.mediaIds],
});
const cacheKey = "alldownloads.media-selection.v1";

export function createMediaSelectionSession(storage?: Pick<Storage, "getItem" | "setItem">) {
  const saved = reactive(new Map<string, SavedMediaSelection>());
  const revision = ref(0);
  if (storage) {
    try {
      const entries: unknown = JSON.parse(storage.getItem(cacheKey) || "[]");
      if (Array.isArray(entries)) for (const item of entries) {
        if (typeof item?.contentId === "string" && typeof item.contentVersion === "string" &&
            Array.isArray(item.mediaIds) && item.mediaIds.every((id: unknown) => typeof id === "string")) {
          saved.set(item.contentId, { ...item, mediaIds: [...new Set<string>(item.mediaIds)] });
        }
      }
    } catch { /* Invalid or unavailable browser cache cannot invent a selection. */ }
  }
  function get(contentId: string) {
    const value = saved.get(contentId);
    return value ? copy(value) : undefined;
  }
  function save(value: SavedMediaSelection) {
    const next = { ...value, mediaIds: [...new Set(value.mediaIds)] };
    saved.set(value.contentId, copy(next));
    revision.value++;
    try { storage?.setItem(cacheKey, JSON.stringify([...saved.values()])); }
    catch { /* In-memory choices still last until this document is closed. */ }
    return copy(next);
  }
  function draft(contentId: string, contentVersion: string, media: readonly SelectableMedia[]) {
    const previous = get(contentId);
    // Explicitly empty is a saved choice. Never fall back to all on length === 0.
    return previous ?? { contentId, contentVersion, mediaIds: defaultMediaIds(media) };
  }
  function snapshotsFor(contentIds: readonly string[]) {
    return [...new Set(contentIds)].flatMap((id) => {
      const value = get(id);
      return value ? [value] : [];
    });
  }
  return { get, save, draft, snapshotsFor, revision: readonly(revision),
    entries: computed(() => [...saved.values()].map(copy)) };
}

function sessionCache() {
  try { return typeof sessionStorage === "undefined" ? undefined : sessionStorage; }
  catch { return undefined; }
}

/** Shared by the detail drawer and preview; creating a draft never mutates it. */
export const mediaSelectionSession = createMediaSelectionSession(sessionCache());
