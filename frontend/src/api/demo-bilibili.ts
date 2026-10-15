import type { BilibiliContent, BilibiliSource, DownloadTarget, MediaTrack, MediaUnit, RecordData, Selection } from "./types";
import { previewAction } from "../preview-presentation";

const copy = <T>(value: T): T => JSON.parse(JSON.stringify(value));
const fail = (code: string, message: string): never => { throw Object.assign(new Error(message), { code }); };
const kinds: Record<string, string> = { FAVORITE: "收藏夹", COLLECTION: "合集", USER_ADDED: "用户添加视频", SERIES: "番剧", MOVIE: "电影" };
const outputKey = (output: RecordData) => output.kind + ":" + [...output.trackIds].sort().join(",");

export function demoSource(source: RecordData): BilibiliSource {
  return { id: source.id, platform: "BILIBILI", kind: source.kind, title: source.title, alias: source.alias || null,
    url: source.url, status: source.status, lastSyncedAt: source.lastSyncedAt, itemCount: source.itemCount,
    revision: source.revision, pinned: source.pinned,
    navigationGroup: source.kind === "FAVORITE" ? "FAVORITES" : source.kind === "COLLECTION" ? "COLLECTIONS" : "OTHER",
    displayLabel: kinds[source.kind] + "-" + (source.alias || source.title) };
}

/** Synthetic public metadata only. No remote media requests or inferred live capability. */
export function seedMedia(contents: RecordData[], parts: RecordData[]) {
  for (const c of contents) {
    c.kind = c.type === "VIDEO" ? "SINGLE" : c.type;
    c.revision = "demo-v1";
    c.externalIdType = c.type === "SERIES" ? "SEASON_ID" : c.type === "MOVIE" ? "MEDIA_ID" : "BVID";
    c.externalId = c.type === "SERIES" ? "ss300" : c.type === "MOVIE" ? "md400" : "BV1Demo" + c.id;
    c.seasonId = c.type === "SERIES" ? "300" : null;
    c.originalUrl = c.type === "SERIES" ? "https://www.bilibili.com/bangumi/play/ss300"
      : c.type === "MOVIE" ? "https://www.bilibili.com/bangumi/media/md400"
      : "https://www.bilibili.com/video/" + c.externalId;
    c.warnings = ["虚构演示媒体，用于验证选择；未查询真实平台。"];
    c.demoLocalOutputs = [];
    c.demoArchiveOutputs = [];
    c.demoActiveOutputs = [];
  }
  const movie = contents.find((c) => c.type === "MOVIE");
  if (movie) {
    const unit = parts.find((p) => p.contentId === movie.id)!;
    unit.kind = "LANGUAGE"; unit.title = "普通话"; unit.language = "zh-CN"; unit.languageLabel = "普通话";
    parts.push({ ...unit, id: movie.id + "-p2", externalId: "demo-movie-yue", index: 2,
      title: "粤语", language: "yue", languageLabel: "粤语" });
    movie.downloadSummary.totalCount = 2;
  }
  for (const p of parts) {
    const c = contents.find((entry) => entry.id === p.contentId)!;
    p.language ??= null; p.languageLabel ??= null;
    p.availabilityReason = c.availabilityReason;
    if (p.kind === "PART") p.title = ["开始探索", "深入理解", "实践与回顾"][p.index - 1] || "";
    if (p.kind === "EPISODE") p.title = ["开始探索", "深入理解", "实践与回顾", "走向远方"][p.index - 1];
    const track = (suffix: string, kind: MediaTrack["kind"], extra: Partial<MediaTrack> = {}): MediaTrack => ({
      id: p.id + "-" + suffix, kind, available: c.availability === "AVAILABLE",
      reasonCode: c.availability === "AVAILABLE" ? null : "PERMISSION_DENIED",
      language: null, languageLabel: null, subtitleOrigin: "NOT_APPLICABLE",
      durationSeconds: kind === "VIDEO" || kind === "AUDIO" ? p.durationSeconds : null, sizeBytes: null, ...extra,
    });
    p.tracks = [track("video", "VIDEO"), track("audio", "AUDIO", { language: p.language, languageLabel: p.languageLabel })];
    if (c.id !== "b3") p.tracks.push(track("sub-zh", "SUBTITLE", {
      subtitleOrigin: "HUMAN", language: "zh-CN", languageLabel: "简体中文", sizeBytes: c.id === "b1" ? "3480" : null,
    }));
    if (c.id === "b1") p.tracks.push(track("sub-en", "SUBTITLE", {
      subtitleOrigin: "HUMAN", language: "en", languageLabel: "English", sizeBytes: "4821",
    }));
    p.tracks.push(track("danmaku", "DANMAKU"));
    const signatures = [outputKey({ kind: "MUXED", trackIds: [p.id + "-video", p.id + "-audio"] }),
      ...p.tracks.filter((t: MediaTrack) => t.kind === "SUBTITLE" || t.kind === "DANMAKU")
        .map((t: MediaTrack) => outputKey({ kind: t.kind, trackIds: [t.id] }))];
    if (c.downloadSummary.state === "VERIFIED") c.demoLocalOutputs.push(...signatures);
    if (c.archiveSummary.state === "VERIFIED") c.demoArchiveOutputs.push(...signatures);
    if (c.downloadSummary.state === "DOWNLOADING") c.demoActiveOutputs.push(...signatures);
    if (c.downloadSummary.state === "PARTIAL" && p.index === 1) c.demoLocalOutputs.push(outputKey({ kind: "AUDIO", trackIds: [p.id + "-audio"] }));
    // Fixed synthetic path received from the demo transport; production paths come from Java.
    const sourceDirectory = ({ s1: "影像收藏(收藏夹)", s2: "灵感与知识(合集)", s3: "宇宙的尺度(番剧)", s4: "用户添加视频(用户添加视频)", s5: "电影(电影)" } as Record<string, string>)[c.sourceIds[0]];
    p.demoDirectory = "Bilibili/" + sourceDirectory + "/" + c.title + "(" + c.externalId + ")" +
      (p.kind === "PART" ? "/P" + String(p.index).padStart(2, "0") + (p.title ? "-" + p.title : "")
        : p.kind === "MAIN" ? "" : "/" + p.title);
  }
}

export function demoContent(c: RecordData, sources: RecordData[], parts: RecordData[]): BilibiliContent {
  const mediaUnits = parts.filter((unit) => unit.contentId === c.id).map((unit): MediaUnit => ({
    id: unit.id, contentId: unit.contentId, externalId: unit.externalId, title: unit.title, index: unit.index,
    kind: unit.kind, language: unit.language, languageLabel: unit.languageLabel,
    durationSeconds: unit.durationSeconds, availability: unit.availability, availabilityReason: unit.availabilityReason,
    tracks: copy(unit.tracks.filter((track: MediaTrack) => track.kind !== "SUBTITLE" || track.subtitleOrigin === "HUMAN")),
  }));
  return { id: c.id, platform: "BILIBILI", externalId: c.externalId, externalIdType: c.externalIdType, kind: c.kind,
    revision: c.revision, title: c.title, coverUrl: c.coverUrl, sourceIds: [...c.sourceIds], originalUrl: c.originalUrl,
    availability: c.availability, availabilityReason: c.availabilityReason, downloadSummary: copy(c.downloadSummary),
    archiveSummary: copy(c.archiveSummary), updatedAt: c.updatedAt, publishedAt: c.publishedAt,
    author: typeof c.author === "string" ? { id: "demo-author-" + c.id, name: c.author } : c.author,
    description: c.description, seasonId: c.seasonId, sources: sources.filter((s) => c.sourceIds.includes(s.id)).map(demoSource),
    mediaUnits, warnings: [...c.warnings, ...(parts.some((unit) => unit.contentId === c.id && unit.tracks.some((track: MediaTrack) =>
      track.kind === "SUBTITLE" && track.subtitleOrigin === "UNKNOWN")) ? ["部分字幕来源尚未判明，暂不可选择。"] : [])] };
}

export function filterDemoContents(contents: RecordData[], filters: RecordData) {
  const from = filters.from ? Date.parse(filters.from + "T00:00:00+08:00") : null;
  const to = filters.to ? Date.parse(filters.to + "T00:00:00+08:00") + 86400000 : null;
  if ((from !== null && !Number.isFinite(from)) || (to !== null && !Number.isFinite(to))) fail("INVALID_INPUT", "日期格式无效");
  return contents.filter((c) => (!filters.sourceId || c.sourceIds.includes(filters.sourceId)) &&
    (!filters.q || c.title.toLowerCase().includes(filters.q.toLowerCase())) &&
    (!filters.type || c.kind === filters.type || (filters.type === "VIDEO" && c.kind === "SINGLE")) &&
    (!filters.state || c.downloadSummary.state === filters.state) &&
    (from === null || (c.publishedAt !== null && Date.parse(c.publishedAt) >= from)) &&
    (to === null || (c.publishedAt !== null && Date.parse(c.publishedAt) < to)));
}
export function sortedContentPage(contents: RecordData[], params: URLSearchParams, catalogueRevision: string) {
  const filters = Object.fromEntries([...params].filter(([key]) => key !== "cursor" && key !== "pageSize"));
  const sort = params.get("sort") || "PUBLISHED_DESC";
  if (!["TITLE_ASC", "TITLE_DESC", "PUBLISHED_DESC", "PUBLISHED_ASC"].includes(sort)) fail("INVALID_INPUT", "不支持的排序方式");
  const signature = JSON.stringify([Object.entries(filters).sort(), catalogueRevision]);
  let offset = 0;
  if (params.get("cursor")) {
    try {
      const cursor = JSON.parse(decodeURIComponent(params.get("cursor")!));
      if (cursor.signature !== signature) fail("VERSION_CHANGED", "列表条件或内容已变化，请从第一页重新加载");
      if (!Number.isInteger(cursor.offset) || cursor.offset < 0) fail("INVALID_INPUT", "分页游标无效");
      offset = cursor.offset;
    } catch (error) {
      if ((error as { code?: string }).code) throw error;
      fail("INVALID_INPUT", "分页游标无效");
    }
  }
  const compare = (a: string, b: string) => a < b ? -1 : a > b ? 1 : 0;
  const data = filterDemoContents(contents, filters).sort((a, b) => {
    let order = 0;
    if (sort.startsWith("TITLE")) order = compare(a.title, b.title) * (sort === "TITLE_ASC" ? 1 : -1);
    else if (a.publishedAt === null || b.publishedAt === null) order = a.publishedAt === b.publishedAt ? 0 : a.publishedAt === null ? 1 : -1;
    else order = compare(a.publishedAt, b.publishedAt) * (sort === "PUBLISHED_ASC" ? 1 : -1);
    return order || compare(a.id, b.id);
  });
  const pageSize = Math.max(1, Math.min(200, Number(params.get("pageSize") || 50)));
  return { items: data.slice(offset, offset + pageSize), total: data.length,
    nextCursor: offset + pageSize < data.length ? encodeURIComponent(JSON.stringify({ offset: offset + pageSize, signature })) : null };
}

export function defaultDemoTarget(settings: RecordData): DownloadTarget {
  return { id: "target-default", revision: settings.storageRevision, status: "VALID", path: settings.downloadRoot,
    label: settings.downloadRoot, rootId: "demo-default-root", archivePolicy: "AUTO", error: null };
}

export function demoBilibiliPreview(contents: RecordData[], parts: RecordData[], selection: Selection, ids: string[], target: DownloadTarget) {
  const selectedParts = parts.filter((part) => selection.resource === "BILIBILI_PARTS" ? ids.includes(part.id) : ids.includes(part.contentId));
  const contentIds = new Set(selectedParts.map((part) => part.contentId));
  const choices = new Map((selection.mediaSelections ?? []).map((choice) => [choice.contentId, choice]));
  if (choices.size !== (selection.mediaSelections ?? []).length) fail("INVALID_INPUT", "内容选择重复");
  for (const [contentId, choice] of choices) {
    if (!contentIds.has(contentId)) fail("INVALID_INPUT", "媒体选择超出本次内容范围");
    const content = contents.find((c) => c.id === contentId)!;
    if (choice.contentRevision !== content.revision) fail("VERSION_CHANGED", "内容版本已变化，请打开详情核对并重新保存");
    if (!choice.trackIds.length) fail("EMPTY_SELECTION", "已保存的媒体选择为空，请打开详情重新选择");
    const tracks: MediaTrack[] = selectedParts.filter((p) => p.contentId === contentId).flatMap((p) => p.tracks);
    if (choice.trackIds.some((id) => !tracks.some((track) => track.id === id && track.available &&
      (track.kind !== "SUBTITLE" || track.subtitleOrigin === "HUMAN")))) fail("INVALID_INPUT", "包含不可用或范围以外的媒体选项，请核对详情");
  }
  return selectedParts.flatMap((part) => {
    const content = contents.find((c) => c.id === part.contentId)!;
    const choice = choices.get(content.id);
    const tracks: MediaTrack[] = part.tracks.filter((track: MediaTrack) =>
      track.available && (track.kind !== "SUBTITLE" || track.subtitleOrigin === "HUMAN") && (!choice || choice.trackIds.includes(track.id)));
    if (!tracks.length && choice) return [];
    const video = tracks.filter((track) => track.kind === "VIDEO"), audio = tracks.filter((track) => track.kind === "AUDIO");
    const mux = video.length > 0 && audio.length > 0;
    const outputs: RecordData[] = [];
    const output = (suffix: string, kind: string, selected: MediaTrack[], sizeBytes: string | null = null) =>
      outputs.push({ id: part.id + "-" + suffix, relativePath: part.demoDirectory + "/" + suffix,
        kind, trackIds: selected.map((track) => track.id), sizeBytes });
    if (mux) output("media.mp4", "MUXED", [...video, ...audio]);
    else { if (video.length) output("video.mp4", "VIDEO", video); if (audio.length) output("audio.m4a", "AUDIO", audio); }
    for (const track of tracks.filter((t) => t.kind === "SUBTITLE" || t.kind === "DANMAKU"))
      output(track.kind === "SUBTITLE" ? (track.language || "und") + "-" + track.id + ".srt" : "danmaku.xml", track.kind, [track], track.sizeBytes);
    let reasonCode = "PENDING_DOWNLOAD", decision = "DOWNLOAD";
    if (content.availability !== "AVAILABLE") {
      decision = "BLOCKED";
      reasonCode = part.tracks.find((track: MediaTrack) => track.reasonCode)?.reasonCode || "STATE_UNKNOWN";
    } else if (target.archivePolicy === "NONE") reasonCode = "CUSTOM_TARGET_NO_ARCHIVE";
    else if (outputs.length && outputs.every((output) => content.demoActiveOutputs.includes(outputKey(output)))) { reasonCode = "DOWNLOADING"; decision = "REUSE"; }
    else if (outputs.length && outputs.every((output) => content.demoLocalOutputs.includes(outputKey(output)))) {
      reasonCode = outputs.every((output) => content.demoArchiveOutputs.includes(outputKey(output))) ? "ARCHIVE_VERIFIED" : "LOCAL_VERIFIED";
      decision = "REUSE";
    } else if (outputs.some((output) => content.demoLocalOutputs.includes(outputKey(output)))) reasonCode = "PARTIAL_EXISTS";
    const item: RecordData = { id: part.id, contentId: content.id, partId: part.id,
      title: content.title + (part.kind === "MAIN" ? "" : " / " + part.title),
      version: content.revision, relativePath: part.demoDirectory, sizeBytes: outputs.length && outputs.every((o) => o.sizeBytes !== null)
        ? outputs.reduce((sum, o) => sum + BigInt(o.sizeBytes), 0n).toString() : null,
      decision, reasonCode, reason: content.availabilityReason || "", selectedTrackIds: tracks.map((track) => track.id),
      mergePolicy: mux ? "MUX" : "SEPARATE", outputs,
      downloadState: ["LOCAL_VERIFIED", "ARCHIVE_VERIFIED"].includes(reasonCode) ? "VERIFIED" : content.downloadSummary.state,
      archiveState: reasonCode === "ARCHIVE_VERIFIED" ? "VERIFIED" : content.archiveSummary.state };
    item.actionLabel = previewAction(item, target.archivePolicy);
    return [item];
  });
}
