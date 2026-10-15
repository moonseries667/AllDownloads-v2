import type { DownloadTarget, RecordData, Selection } from "./types";

// Isolated Quark facts for the shared preview contract (6233234 / 9e7886f).
// The browser never executes these paths or performs platform I/O.
export function demoQuarkPreview(nodes: RecordData[], shares: RecordData[], selection: Selection, target: DownloadTarget): RecordData[] {
  if (selection.resource !== "QUARK_NODES") throw Object.assign(new Error("夸克下载必须选择网盘节点"), { code: "INVALID_INPUT" });
  const ids = selection.mode === "QUERY"
    ? nodes.filter((node) => (!selection.filters?.shareId || node.shareId === selection.filters.shareId) &&
      (!selection.filters?.q || node.name.includes(selection.filters.q)) && !selection.excludeIds?.includes(node.id)).map((node) => node.id)
    : selection.ids ?? [];
  const selected = new Set(ids);
  if (ids.some((id) => !nodes.some((node) => node.id === id))) throw Object.assign(new Error("所选文件夹或文件已变化，请重新选择"), { code: "VERSION_CHANGED" });
  let grew = true;
  while (grew) {
    grew = false;
    for (const node of nodes) if (node.parentId && selected.has(node.parentId) && !selected.has(node.id)) {
      selected.add(node.id);
      grew = true;
    }
  }
  return nodes.filter((node) => node.kind === "FILE" && selected.has(node.id)).map((node) => {
    const share = shares.find((entry) => entry.id === node.shareId);
    if (!share) throw Object.assign(new Error("文件的分享归属已变化，请刷新"), { code: "VERSION_CHANGED" });
    const available = node.availability === "AVAILABLE";
    const local = target.archivePolicy === "AUTO" && node.downloadSummary?.state === "VERIFIED";
    const active = target.archivePolicy === "AUTO" && ["DOWNLOADING", "QUEUED"].includes(node.downloadSummary?.state);
    const reasonCode = !available ? (node.availability === "REMOVED" ? "RESOURCE_DELETED" : "STATE_UNKNOWN")
      : local ? "LOCAL_VERIFIED" : active ? "DOWNLOADING" : target.archivePolicy === "NONE" ? "CUSTOM_TARGET_NO_ARCHIVE" : "PENDING_DOWNLOAD";
    const relativePath = "Quark/" + share.shareFolderKey + "/" + node.relativePath;
    return {
      id: node.id, nodeId: node.id, contentId: null, partId: null, shareId: node.shareId,
      version: node.version, title: node.name, relativePath, sizeBytes: node.sizeBytes ?? null,
      decision: !available ? "BLOCKED" : local || active ? "REUSE" : "DOWNLOAD",
      reasonCode, reason: !available ? node.availabilityReason ?? "源文件状态尚未确认，请刷新信息" : null,
      actionLabel: local ? "已下载，无需下载" : target.archivePolicy === "NONE" ? "仅下载到指定目录，不归档" : "下载所选内容",
      downloadState: local ? "VERIFIED" : active ? "DOWNLOADING" : "NOT_REQUESTED", archiveState: "NOT_REQUESTED",
      selectedTrackIds: [], mergePolicy: "SEPARATE",
      outputs: [{ id: "output-" + node.id, nodeId: node.id, relativePath, trackIds: [], kind: "FILE", sizeBytes: node.sizeBytes ?? null }],
    };
  });
}
