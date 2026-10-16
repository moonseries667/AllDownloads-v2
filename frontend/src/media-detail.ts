import type { SelectableMedia } from "./media-selection";
import type { BilibiliContent } from "./api/types";

/** Presentation models; the API adapter owns conversion from the shared contract. */
export interface DetailMedia extends SelectableMedia {
  label: string;
  languageName: string | null;
  durationSeconds: number | null;
  sizeBytes: string | null;
  availabilityReason: string | null;
}
export interface DetailMediaGroup {
  id: string;
  label: string;
  collapsible: boolean;
  media: DetailMedia[];
  warnings: string[];
}

export function detailMediaGroups(content: BilibiliContent): DetailMediaGroup[] {
  return content.mediaUnits.map((unit) => ({
    id: unit.id,
    label: unit.kind === "EPISODE" ? `第 ${unit.index} 集${unit.title ? " · " + unit.title : ""}`
      : unit.kind === "PART" ? `P${unit.index}${unit.title ? " · " + unit.title : ""}`
      : unit.kind === "LANGUAGE" ? unit.languageLabel || unit.title || "语言未标明"
      : unit.title || "正片",
    collapsible: content.kind === "MULTIPART" || content.kind === "SERIES" ||
      (content.kind === "MOVIE" && content.mediaUnits.length > 1),
    media: unit.tracks.map((track) => ({
      id: track.id, kind: track.kind,
      availability: unit.availability === "AVAILABLE" && track.available ? "AVAILABLE" : "UNAVAILABLE",
      subtitleKind: track.subtitleOrigin === "NOT_APPLICABLE" ? null : track.subtitleOrigin,
      label: ({ VIDEO: "视频", AUDIO: "音频", SUBTITLE: "字幕", DANMAKU: "弹幕" }[track.kind]) +
        (track.languageLabel || track.language ? " · " + (track.languageLabel || track.language) : ""),
      languageName: track.languageLabel,
      durationSeconds: track.durationSeconds,
      sizeBytes: track.sizeBytes,
      availabilityReason: unit.availabilityReason || mediaReason(track.reasonCode),
    })),
    warnings: unit.tracks.some((track) => track.kind === "SUBTITLE" && track.subtitleOrigin === "UNKNOWN")
      ? ["部分字幕来源尚未判明，暂不可选择。"] : [],
  }));
}

export function mediaReason(code: string | null) {
  if (!code) return null;
  return ({ PERMISSION_DENIED: "无访问权限", RESOURCE_DELETED: "原视频已删除", REGION_RESTRICTED: "地区限制",
    PAYMENT_REQUIRED: "需要购买", NETWORK_TEMPORARY: "暂时获取失败", STATE_UNKNOWN: "状态待确认",
    NOT_AVAILABLE: "源站未提供此项" } as Record<string, string>)[code] || "暂不可下载，请刷新信息";
}
