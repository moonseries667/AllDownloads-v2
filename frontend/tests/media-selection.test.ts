import { describe, expect, it } from "vitest";
import { createMediaSelectionSession, defaultMediaIds, type SelectableMedia } from "../src/media-selection";

const media: SelectableMedia[] = [
  { id: "video", kind: "VIDEO", availability: "AVAILABLE" },
  { id: "audio", kind: "AUDIO", availability: "AVAILABLE" },
  { id: "zh", kind: "SUBTITLE", subtitleKind: "HUMAN", availability: "AVAILABLE" },
  { id: "en", kind: "SUBTITLE", subtitleKind: "HUMAN", availability: "AVAILABLE" },
  { id: "ai", kind: "SUBTITLE", subtitleKind: "AI", availability: "AVAILABLE" },
  { id: "unknown", kind: "SUBTITLE", subtitleKind: "UNKNOWN", availability: "AVAILABLE" },
  { id: "danmaku", kind: "DANMAKU", availability: "AVAILABLE" },
  { id: "locked", kind: "AUDIO", availability: "UNAVAILABLE" },
];

describe("session media choices", () => {
  it("defaults to all available media and human languages only", () => {
    expect(defaultMediaIds(media)).toEqual(["video", "audio", "zh", "en", "danmaku"]);
  });
  it("discards unsaved draft changes and preserves an explicit empty save", () => {
    const session = createMediaSelectionSession();
    const draft = session.draft("same-content", "v1", media);
    draft.mediaIds.splice(0);
    expect(session.get("same-content")).toBeUndefined();
    session.save(draft);
    expect(session.draft("same-content", "v1", media).mediaIds).toEqual([]);
  });
  it("does not expand a custom selection on source/page changes or a newer content version", () => {
    const session = createMediaSelectionSession();
    session.save({ contentId: "same-content", contentVersion: "v1", mediaIds: ["en"] });
    expect(session.draft("same-content", "v2", media)).toEqual({
      contentId: "same-content", contentVersion: "v1", mediaIds: ["en"],
    });
    expect(session.snapshotsFor(["same-content", "same-content"])).toHaveLength(1);
    expect(session.snapshotsFor(["another-season"])).toEqual([]);
  });
  it("publishes saved changes without allowing a consumer to mutate the saved snapshot", () => {
    const session = createMediaSelectionSession();
    session.save({ contentId: "c", contentVersion: "v1", mediaIds: ["audio"] });
    const queued = session.snapshotsFor(["c"]);
    queued[0].mediaIds.push("video");
    expect(session.get("c")?.mediaIds).toEqual(["audio"]);
    session.save({ contentId: "c", contentVersion: "v1", mediaIds: ["zh"] });
    expect(session.revision.value).toBe(2);
    expect(session.snapshotsFor(["c"])[0].mediaIds).toEqual(["zh"]);
  });
  it("restores saved empty and custom choices for the same browser session", () => {
    const values = new Map<string, string>();
    const storage = { getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => { values.set(key, value); } };
    createMediaSelectionSession(storage).save({ contentId: "c", contentVersion: "v1", mediaIds: [] });
    expect(createMediaSelectionSession(storage).draft("c", "v1", media).mediaIds).toEqual([]);
  });
});
