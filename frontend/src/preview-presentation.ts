import type { RecordData } from "./api/types";

/** A reused file is not necessarily a verified cloud archive. */
export function previewFact(item: RecordData) {
  switch (item.reasonCode) {
    case "ARCHIVE_VERIFIED": return item.archiveState === "VERIFIED" ? "已归档" : "状态待确认";
    case "LOCAL_VERIFIED": return item.downloadState === "VERIFIED" ? "已下载" : "状态待确认";
    case "DOWNLOADING": return "正在下载";
    case "RESOURCE_DELETED": return "原视频已删除";
    case "PERMISSION_DENIED": return "无访问权限";
    case "REGION_RESTRICTED": return "地区限制";
    case "PAYMENT_REQUIRED": return "需要购买";
    case "NETWORK_TEMPORARY": return "暂时获取失败";
    case "PARTIAL_EXISTS": return "部分内容已下载";
    case "PENDING_DOWNLOAD": case "CUSTOM_TARGET_NO_ARCHIVE": return "待下载";
    default: return "状态待确认";
  }
}

export function previewAction(item: RecordData, archivePolicy?: string) {
  if (item.decision === "DOWNLOAD") {
    if (archivePolicy === "NONE") return "仅下载到指定目录，不归档";
    return item.reasonCode === "PARTIAL_EXISTS" ? "补齐尚未下载的内容" : "下载所选内容";
  }
  const fact = previewFact(item);
  if (fact === "已归档" || fact === "已下载") return fact + "，无需下载";
  if (fact === "正在下载") return "正在下载，不重复创建";
  if (["原视频已删除", "无访问权限", "地区限制", "需要购买"].includes(fact)) return fact + "，不能下载";
  if (fact === "暂时获取失败") return "暂时获取失败，请稍后刷新";
  return "状态待确认，请刷新信息";
}

export function hasReliableSizes(items: RecordData[]) {
  return items.length > 0 && items.every((item) => typeof item.sizeBytes === "string" && /^\d+$/.test(item.sizeBytes));
}

export function previewCounts(items: RecordData[]) {
  const result = new Map<string, number>();
  for (const item of items) {
    const fact = previewFact(item);
    result.set(fact, (result.get(fact) ?? 0) + 1);
  }
  return [...result].map(([label, count]) => ({ label, count }));
}
