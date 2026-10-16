// Consumer mapping for contracts/FEEDBACK-PART3.md / management.schema.json, baa03f5.
import { api } from "./client";
import type { Platform } from "./types";
import type { DownloadDetail, DownloadManagement, DownloadPart, DownloadProject } from "../download-management";

export interface ManagedOutput { id: string; relativePath: string; kind: string; trackIds?: string[]; sizeBytes: string | null }
export interface ManagedFile { outputId: string; relativePath: string; verified: boolean; sizeBytes: string; mediaKinds?: string[] }
export interface ManagedControl {
  id: string; revision: string; status: string; desiredAction: string; allowedActions: string[]; actionReason: string | null;
}
export interface ManagedChild extends ManagedControl {
  projectId: string; contentId: string; partId: string | null; attachmentId: string | null; nodeId: string | null;
  title: string; taskIds: string[]; outputs: ManagedOutput[]; files: ManagedFile[];
}
export interface ManagedProject extends ManagedControl {
  batchId: string; platform: Platform; contentId: string; title: string; createdAt: string;
  target: Record<string, unknown>; archivePolicy: "AUTO" | "NONE"; archiveTarget: Record<string, unknown> | null;
  summary: { total: number; completed: number; running: number; failed: number; cancelled: number; pending: number };
  items: ManagedChild[];
}
const pathFor = (id: string) => "/download-projects/" + encodeURIComponent(id);
function control(source: ManagedControl) {
  if (!source || typeof source.id !== "string" || typeof source.revision !== "string" || !Array.isArray(source.allowedActions) || typeof source.status !== "string")
    throw new Error("下载项目响应缺少身份、版本或操作能力，请检查接口版本。");
  return { id: source.id, revision: source.revision, state: source.status, desiredAction: source.desiredAction,
    allowedActions: [...source.allowedActions], cancelUnavailableReason: source.actionReason };
}
export function projectView(project: ManagedProject): DownloadProject {
  const base = control(project);
  if (!project.summary || !Array.isArray(project.items) || typeof project.batchId !== "string" || typeof project.contentId !== "string")
    throw new Error("下载项目响应缺少批次、内容身份或冻结范围，请检查接口版本。");
  return { ...base, batchId: project.batchId, platform: project.platform, contentId: project.contentId,
    title: project.title, createdAt: project.createdAt, completedCount: project.summary.completed,
    totalCount: project.summary.total, failedCount: project.summary.failed, cancelledCount: project.summary.cancelled };
}
export function detailView(project: ManagedProject): DownloadDetail {
  const view = projectView(project);
  return { ...view, parts: project.items.map((child): DownloadPart => {
    if (child.projectId !== project.id) throw new Error("下载明细与当前实例不匹配，请刷新后重试。");
    const files = child.outputs.map(output => ({ id: output.id, relativePath: output.relativePath,
      state: child.files.some(file => file.outputId === output.id && file.verified) ? "VERIFIED" : child.status === "SUCCEEDED" ? "UNKNOWN" : child.status }));
    const muxed = child.outputs.some(output => output.kind === "MUXED");
    return { ...control(child), title: child.title,
      kind: project.platform === "BILIBILI" ? "媒体部分" : project.platform === "TELEGRAM" ? "消息附件" : "分享文件",
      relativePath: null, totalCount: files.length, completedCount: files.filter(file => file.state === "VERIFIED").length, files,
      cancelImpact: muxed ? "此部分包含合并成果。取消将一并停止其音视频下载和合并；其他部分继续，已完成文件保留。"
        : project.platform === "TELEGRAM" ? "取消此附件及对应侧车的未完成处理；同一媒体组的其他文件继续。" : null };
  }) };
}
export function createDownloadManagementApi(client: Pick<typeof api, "get" | "post">): DownloadManagement {
  return {
    async list(filters, cursor) {
      if (filters.sourceId) throw new Error("下载项目接口不支持按来源筛选，请清除来源筛选或从具体内容查看记录。");
      const query = new URLSearchParams({ pageSize: "50" });
      for (const [key, value] of Object.entries({ platform: filters.platform, contentId: filters.contentId, cursor })) if (value) query.set(key, value);
      const page = await client.get<{ items: ManagedProject[]; nextCursor: string | null; total: number | null }>("/download-projects?" + query);
      return { ...page, items: page.items.map(projectView) };
    },
    async detail(id) { return detailView(await client.get<ManagedProject>(pathFor(id))); },
    cancelProject: project => client.post(pathFor(project.id) + "/actions", { action: "CANCEL" }, project.revision),
    cancelPart: (projectId, part) => client.post(pathFor(projectId) + "/items/" + encodeURIComponent(part.id) + "/actions", { action: "CANCEL" }, part.revision),
  };
}
export const downloadManagementApi = createDownloadManagementApi(api);
