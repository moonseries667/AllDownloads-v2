import type { InjectionKey } from "vue";
import type { Platform } from "./api/types";

/** UI model. Wire responses are mapped by api/download-management.ts, not inferred in the page. */
export interface DownloadControl {
  id: string;
  revision: string;
  state: string;
  desiredAction: string;
  allowedActions: string[];
  cancelUnavailableReason: string | null;
}
export interface DownloadPart extends DownloadControl {
  title: string;
  kind: string;
  relativePath: string | null;
  cancelImpact: string | null;
  completedCount: number;
  totalCount: number;
  files: { id: string; relativePath: string; state: string }[];
}
export interface DownloadProject extends DownloadControl {
  batchId: string;
  platform: Platform;
  contentId: string;
  title: string;
  createdAt: string;
  completedCount: number;
  totalCount: number;
  failedCount: number;
  cancelledCount: number;
}
export interface DownloadDetail extends DownloadProject { parts: DownloadPart[]; }
export interface DownloadFilters { platform?: string; contentId?: string; sourceId?: string; state?: string; q?: string }
export interface DownloadPage { items: DownloadProject[]; nextCursor: string | null; total: number | null }
export interface DownloadManagement {
  list(filters: DownloadFilters, cursor?: string): Promise<DownloadPage>;
  detail(id: string): Promise<DownloadDetail>;
  cancelProject(project: DownloadProject): Promise<unknown>;
  cancelPart(projectId: string, part: DownloadPart): Promise<unknown>;
}
export const downloadManagementKey: InjectionKey<DownloadManagement> = Symbol("download-management");
export function finished(control: DownloadControl) {
  return ["SUCCEEDED", "VERIFIED", "CANCELLED", "FAILED", "PARTIAL"].includes(control.state) && !control.allowedActions.includes("CANCEL");
}
export function cancelling(control: DownloadControl) { return control.desiredAction === "CANCEL" && !finished(control); }
export function cancelReason(control: DownloadControl) {
  if (cancelling(control)) return "取消请求已受理，等待执行者确认停止。";
  if (control.allowedActions.includes("CANCEL")) return "";
  return control.cancelUnavailableReason || (finished(control) ? "当前范围已结束；已完成文件保留。" : "当前状态不允许取消，请刷新状态后查看。");
}
export function downloadHistoryLocation(contentId: string) {
  return { path: "/downloads", query: { platform: "BILIBILI", contentId } };
}
