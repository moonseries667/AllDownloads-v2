// Presentation model only; the Java upload query owns admission to this list.
export type UploadAction = "PAUSE" | "RESUME" | "RETRY" | "RECONCILE";
export type UploadGroup = "pending" | "active" | "attention" | "completed";
export interface UploadRow {
  id: string;
  title: string;
  platform: string;
  instanceLabel: string;
  state: string;
  desiredAction: string;
  destination: string;
  doneBytes: string | null;
  totalBytes: string | null;
  doneItems: number;
  totalItems: number | null;
  actions: UploadAction[];
  error: string | null;
  updatedAt: string | null;
}
export const uploadGroups: { value: UploadGroup; label: string }[] = [
  { value: "pending", label: "待上传" },
  { value: "active", label: "上传中 / 核验中" },
  { value: "attention", label: "需要处理" },
  { value: "completed", label: "上传完成" },
];
export const uploadActionLabel: Record<UploadAction, string> = {
  PAUSE: "暂停上传", RESUME: "继续上传", RETRY: "重试上传", RECONCILE: "核对结果",
};
export function uploadGroup(state: string): UploadGroup {
  if (state === "VERIFIED") return "completed";
  if (["UPLOADING", "VERIFYING", "RUNNING", "PAUSING"].includes(state)) return "active";
  if (["QUEUED", "PAUSED", "WAITING_RETRY"].includes(state)) return "pending";
  return "attention";
}
export function uploadStateLabel(row: Pick<UploadRow, "state" | "desiredAction">): string {
  if (row.desiredAction === "PAUSE" && !["PAUSED", "VERIFIED", "FAILED", "CANCELLED"].includes(row.state)) return "正在暂停";
  return ({ QUEUED: "待上传", UPLOADING: "上传中", RUNNING: "上传中 / 核验中", VERIFYING: "核验中",
    VERIFIED: "上传完成", PAUSED: "已暂停", PAUSING: "正在暂停", WAITING_RETRY: "等待重试",
    NEEDS_ACTION: "需要处理", FAILED: "上传失败", CANCELLED: "已取消" } as Record<string, string>)[row.state] ?? "状态待确认";
}
export function uploadPercent(row: Pick<UploadRow, "doneBytes" | "totalBytes" | "doneItems" | "totalItems">): number | null {
  const done = Number(row.doneBytes), total = Number(row.totalBytes);
  if (row.doneBytes !== null && row.totalBytes !== null && Number.isFinite(done) && Number.isFinite(total) && done >= 0 && total > 0)
    return Math.max(0, Math.min(100, Math.floor(done / total * 100)));
  if (row.totalItems !== null && row.totalItems > 0 && row.doneItems >= 0)
    return Math.max(0, Math.min(100, Math.floor(row.doneItems / row.totalItems * 100)));
  return null;
}
