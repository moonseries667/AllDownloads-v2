import { describe, expect, it } from "vitest";
import { createDemoTransport } from "../src/api/demo";
import { createSeed } from "../src/api/fixtures";
import type { BilibiliContent, BilibiliSource, DownloadTarget, Page, Preview, Selection } from "../src/api/types";
import { detailMediaGroups } from "../src/media-detail";
import { hasReliableSizes, previewAction, previewFact } from "../src/preview-presentation";

function setup() {
  const state = createSeed();
  const transport = createDemoTransport({ persist: false, state });
  const request = transport.request;
  const preview = async (ids: string[], trackIds?: string[], target?: DownloadTarget) => {
    const selection: Selection = { mode: "EXPLICIT", resource: "BILIBILI_CONTENTS", ids };
    if (trackIds) selection.mediaSelections = [{ contentId: ids[0], contentRevision: "demo-v1", trackIds }];
    const receipt = await request<{ previewId: string }>("POST", "/download-previews", { platform: "BILIBILI", selection,
      ...(target ? { target: { id: target.id, revision: target.revision } } : {}) });
    return request<Preview>("GET", "/download-previews/" + receipt.previewId);
  };
  return { state, request, preview };
}

describe("Bilibili frozen media contract d508c7a", () => {
  it("defaults to every actual track and describes mux and known auxiliary sizes", async () => {
    const h = setup();
    const detail = await h.request<BilibiliContent>("GET", "/bilibili/contents/b1");
    expect(detail.sources.map((source) => source.displayLabel)).toEqual(["收藏夹-影像收藏", "合集-灵感与知识"]);
    const preview = await h.preview(["b1"]);
    expect(preview.items[0].selectedTrackIds).toEqual(detail.mediaUnits[0].tracks.map((track) => track.id));
    expect(preview.items[0].mergePolicy).toBe("MUX");
    expect(preview.items[0].outputs.map((output: any) => output.kind)).toEqual(["MUXED", "SUBTITLE", "SUBTITLE", "DANMAKU"]);
    expect(preview.items[0].sizeBytes).toBeNull();
    expect(detail.mediaUnits[0].tracks.find((track) => track.id.endsWith("sub-zh"))?.sizeBytes).toBe("3480");
    expect(h.state.orders).toHaveLength(1);
  });
  it.each([["audio", "AUDIO"], ["video", "VIDEO"], ["sub-en", "SUBTITLE"], ["danmaku", "DANMAKU"]])("freezes a standalone %s output without attaching extra tracks", async (suffix, kind) => {
    const h = setup();
    const pv = await h.preview(["b1"], ["b1-p1-" + suffix]);
    expect(pv.items[0].mergePolicy).toBe("SEPARATE");
    expect(pv.items[0].outputs).toHaveLength(1);
    expect(pv.items[0].outputs[0].kind).toBe(kind);
    const receipt = await h.request<{ orderId: string }>("POST", "/download-orders", { previewId: pv.id });
    const order = await h.request<any>("GET", "/download-orders/" + receipt.orderId);
    expect(order.items[0].selectedTrackIds).toEqual(["b1-p1-" + suffix]);
  });
  it("rejects explicit empty, unknown and out-of-range track selections", async () => {
    const h = setup();
    await expect(h.preview(["b1"], [])).rejects.toMatchObject({ code: "EMPTY_SELECTION" });
    await expect(h.preview(["b1"], ["b3-p1-video"])).rejects.toMatchObject({ code: "INVALID_INPUT" });
  });
  it("excludes AI and unclassified subtitles from public choices and default previews", async () => {
    const h = setup();
    const part = h.state.parts.find((unit) => unit.id === "b1-p1")!;
    for (const origin of ["AI", "UNKNOWN"]) part.tracks.push({ ...part.tracks[2], id: origin, subtitleOrigin: origin });
    const detail = await h.request<BilibiliContent>("GET", "/bilibili/contents/b1");
    expect(detail.mediaUnits[0].tracks.some((track) => ["AI", "UNKNOWN"].includes(track.subtitleOrigin))).toBe(false);
    expect(detail.warnings.join(" ")).toContain("尚未判明");
    expect((await h.preview(["b1"])).items[0].selectedTrackIds).not.toContain("AI");
    await expect(h.preview(["b1"], ["AI"])).rejects.toMatchObject({ code: "INVALID_INPUT" });
    const noSubtitles = await h.request<BilibiliContent>("GET", "/bilibili/contents/b3");
    expect(noSubtitles.mediaUnits[0].tracks.some((track) => track.kind === "SUBTITLE")).toBe(false);
  });
  it("keeps selected parts, season identity and movie languages distinct", async () => {
    const h = setup();
    const selected = await h.preview(["b2"], ["b2-p1-audio", "b2-p3-video"]);
    expect(selected.items.map((item) => item.partId)).toEqual(["b2-p1", "b2-p3"]);
    const series = await h.request<BilibiliContent>("GET", "/bilibili/contents/b7");
    expect(series.externalIdType).toBe("SEASON_ID");
    expect(series.mediaUnits.every((unit) => unit.contentId === series.id && unit.kind === "EPISODE")).toBe(true);
    expect(detailMediaGroups(series).every((unit) => unit.collapsible && unit.label.startsWith("第 "))).toBe(true);
    const movie = await h.request<BilibiliContent>("GET", "/bilibili/contents/b12");
    expect(detailMediaGroups(movie).map((unit) => unit.label)).toEqual(["普通话", "粤语"]);
    expect(detailMediaGroups(movie).every((unit) => unit.collapsible)).toBe(true);
    movie.mediaUnits.pop();
    expect(detailMediaGroups(movie)[0].collapsible).toBe(false);
  });
  it("binds a validated batch target without changing global settings or scheduling an upload", async () => {
    const h = setup();
    const settings = { ...h.state.settings };
    const receipt = await h.request<{ targetId: string }>("POST", "/download-targets/validate", { path: "E:\\本批媒体" });
    const target = await h.request<DownloadTarget>("GET", "/download-targets/" + receipt.targetId);
    const pv = await h.preview(["b9"], undefined, target);
    expect(pv.archivePolicy).toBe("NONE");
    expect(pv.items[0].decision).toBe("DOWNLOAD");
    expect(pv.items[0].reasonCode).toBe("CUSTOM_TARGET_NO_ARCHIVE");
    expect(pv.items[0].downloadState).toBe("NOT_REQUESTED");
    expect(pv.items[0].archiveState).toBe("NOT_REQUESTED");
    const submitted = await h.request<any>("POST", "/download-orders", { previewId: pv.id });
    const order = await h.request<any>("GET", "/download-orders/" + submitted.orderId);
    expect(order.target.path).toBe("E:\\本批媒体");
    expect(order.archivePolicy).toBe("NONE");
    expect(h.state.settings).toEqual(settings);
    expect(h.state.tasks.find((task) => task.id === order.taskIds[0])?.kind).toBe("DOWNLOAD");
    expect((await h.preview(["b1"])).archivePolicy).toBe("AUTO");
  });
  it("rejects invalid directories, stale target revisions and changed content snapshots", async () => {
    const h = setup();
    const receipt = await h.request<any>("POST", "/download-targets/validate", { path: "\\\\server\\share" });
    const invalid = await h.request<DownloadTarget>("GET", "/download-targets/" + receipt.targetId);
    expect(invalid.status).toBe("INVALID");
    await expect(h.preview(["b1"], undefined, invalid)).rejects.toMatchObject({ code: "INVALID_STORAGE_ROOT" });
    const pv = await h.preview(["b1"]);
    h.state.contents[0].revision = "demo-v2";
    await expect(h.request("POST", "/download-orders", { previewId: pv.id })).rejects.toMatchObject({ code: "VERSION_CHANGED" });
    await expect(h.preview(["b1"], ["b1-p1-audio"])).rejects.toMatchObject({ code: "VERSION_CHANGED" });
  });
  it("does not confuse local completion, cloud archive, partial selection and access failure", async () => {
    const h = setup();
    const pv = await h.preview(["b4", "b9", "b11"]);
    expect(pv.items.map(previewFact)).toEqual(["已下载", "已归档", "无访问权限"]);
    const audioOnly = await h.preview(["b6"], ["b6-p1-audio"]);
    expect(previewFact(audioOnly.items[0])).toBe("已下载");
    const mux = await h.preview(["b6"], ["b6-p1-audio", "b6-p1-video"]);
    expect(mux.items[0].decision).toBe("DOWNLOAD");
    expect(previewFact({ decision: "REUSE" })).toBe("状态待确认");
    expect(previewFact({ reasonCode: "NETWORK_TEMPORARY" })).not.toBe("原视频已删除");
    h.state.contents.find((content) => content.id === "b11")!.availability = "REMOVED";
    expect(previewFact((await h.preview(["b11"])).items[0])).toBe("原视频已删除");
    expect(previewAction({ decision: "DOWNLOAD" }, "NONE")).toContain("不归档");
  });
  it("shows size only when every item has reliable bytes, including real zero", () => {
    expect(hasReliableSizes([{ sizeBytes: "0" }, { sizeBytes: "120" }])).toBe(true);
    expect(hasReliableSizes([{ sizeBytes: "120" }, { sizeBytes: null }])).toBe(false);
    expect(hasReliableSizes([{ sizeBytes: 0 }])).toBe(false);
  });
  it("sorts globally before paging and binds cursors to filters and sort", async () => {
    const h = setup();
    h.state.contents[0].title = "A"; h.state.contents[1].title = "A";
    const first = await h.request<Page<BilibiliContent>>("GET", "/bilibili/contents?sort=TITLE_ASC&pageSize=1");
    const second = await h.request<Page<BilibiliContent>>("GET", "/bilibili/contents?sort=TITLE_ASC&pageSize=1&cursor=" + encodeURIComponent(first.nextCursor!));
    expect([first.items[0].id, second.items[0].id]).toEqual(["b1", "b2"]);
    await expect(h.request("GET", "/bilibili/contents?sort=TITLE_DESC&cursor=" + encodeURIComponent(first.nextCursor!))).rejects.toMatchObject({ code: "VERSION_CHANGED" });
    const descending = await h.request<Page<BilibiliContent>>("GET", "/bilibili/contents?sort=TITLE_DESC");
    expect(descending.items.at(-1)?.title).toBe("A");
  });
  it("uses Shanghai upload-date boundaries and places missing upload dates last", async () => {
    const h = setup();
    h.state.contents[0].publishedAt = "2026-10-14T16:00:00Z";
    h.state.contents[1].publishedAt = "2026-10-15T15:59:59Z";
    h.state.contents[2].publishedAt = "2026-10-15T16:00:00Z";
    h.state.contents[3].publishedAt = null;
    const day = await h.request<Page<BilibiliContent>>("GET", "/bilibili/contents?from=2026-10-15&to=2026-10-15");
    expect(day.items.map((content) => content.id)).toEqual(["b2", "b1"]);
    for (const sort of ["PUBLISHED_ASC", "PUBLISHED_DESC"]) {
      const page = await h.request<Page<BilibiliContent>>("GET", "/bilibili/contents?sort=" + sort);
      expect(page.items.at(-1)?.id).toBe("b4");
    }
  });
  it("persists pin state with quoted revision checks and exposes truthful runtime unknowns", async () => {
    const h = setup();
    const updated = await h.request<BilibiliSource>("PATCH", "/bilibili/sources/s2", { pinned: true }, { "If-Match": '"1"' });
    expect(updated.pinned).toBe(true);
    expect(updated.revision).toBe("2");
    const sources = await h.request<Page<BilibiliSource>>("GET", "/bilibili/sources");
    expect(sources.items.find((source) => source.id === "s2")?.pinned).toBe(true);
    expect(sources.items.find((source) => source.kind === "SERIES")?.navigationGroup).toBe("OTHER");
    await expect(h.request("PATCH", "/bilibili/sources/s2", { pinned: false }, { "If-Match": '"1"' })).rejects.toMatchObject({ code: "VERSION_CHANGED" });
    const overview = await h.request<any>("GET", "/overview");
    expect(Object.keys(overview).sort()).toEqual(["bilibili", "runtime"]);
    expect(overview.runtime.state).toBe("UNKNOWN");
    expect(overview.runtime.mount.state).toBe("UNKNOWN");
  });
});
