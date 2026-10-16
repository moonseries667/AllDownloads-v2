import { api } from "./client";
import { queryString } from "./feedback";
import type { DownloadTarget, Page } from "./types";
import type { ArchiveUpload, DirectorySelection, DriveFolder, DriveTarget, SettingsValidation } from "./management.generated";
import type { UploadAction, UploadRow } from "../components/upload-queue";
import type { NativeSelection } from "../components/NativeDirectoryField.vue";
export type { ArchiveUpload, DriveFolder, DriveTarget, SettingsValidation } from "./management.generated";
export interface DriveFolderPage extends Page<DriveFolder> {
  parent: DriveFolder | null; breadcrumbs: DriveFolder[]; status: "LOADING" | "READY" | "NEEDS_ACTION";
  taskId: string | null; error: Record<string, unknown> | null;
}
export interface StorageSettings {
  downloadRoot: string; defaultTarget: DownloadTarget; archiveDestination: DriveTarget | null;
  archiveDestinationLabel: string | null; revision: string; storageRevision: string; timezone: string;
  directoryPicker: { available: boolean; reasonCode: string | null; hostId: string | null };
  downloadConcurrency?: number; uploadConcurrency?: number;
}
type Client = Pick<typeof api, "get" | "post" | "patch">;
export function errorMessage(error: Record<string, unknown> | null, fallback: string) {
  return typeof error?.message === "string" ? error.message : fallback;
}
export function archiveRow(upload: ArchiveUpload): UploadRow {
  const progress = upload.progress;
  return {
    id: upload.id, title: upload.title, platform: upload.platform, instanceLabel: `批次 ${upload.batchId}`,
    state: upload.status === "SUCCEEDED" ? "VERIFIED" : upload.status,
    desiredAction: upload.desiredAction, destination: upload.target.path || upload.target.name,
    doneBytes: typeof progress?.doneBytes === "string" ? progress.doneBytes : null,
    totalBytes: typeof progress?.totalBytes === "string" ? progress.totalBytes : null,
    doneItems: typeof progress?.doneItems === "number" ? progress.doneItems : 0,
    totalItems: typeof progress?.totalItems === "number" ? progress.totalItems : null,
    actions: upload.allowedActions.filter((action): action is UploadAction => ["PAUSE", "RESUME", "RETRY", "RECONCILE"].includes(action)),
    error: upload.error ? errorMessage(upload.error, "上传需要处理") : null, updatedAt: null,
  };
}
export function createArchiveSettingsApi(client: Client, wait: (ms: number) => Promise<void> = ms => new Promise(resolve => setTimeout(resolve, ms))) {
  return {
    archives: (filters: { platform?: string; cursor?: string } = {}) => client.get<Page<ArchiveUpload>>("/archives" + queryString({ ...filters, pageSize: 50 })),
    action: (upload: ArchiveUpload, action: UploadAction) => client.post("/tasks/" + encodeURIComponent(upload.taskId) + "/actions", { action }, upload.revision),
    folders: (accountId: string, parentId?: string, cursor?: string) => client.get<DriveFolderPage>("/drive/folders" + queryString({ accountId, parentId, cursor, pageSize: 50 })),
    async selectDirectory(signal: AbortSignal): Promise<NativeSelection & { target?: DownloadTarget }> {
      const accepted = await client.post<{ selectionId: string }>("/directory-selections", {});
      const path = "/directory-selections/" + encodeURIComponent(accepted.selectionId);
      let cancelRequested = false;
      for (let attempt = 0; attempt < 65; attempt++) {
        if (signal.aborted && !cancelRequested) { await client.post(path + "/cancel", {}); cancelRequested = true; }
        const selection = await client.get<DirectorySelection>(path);
        if (selection.status === "CANCELLED") return { status: "CANCELLED" };
        if (selection.status === "UNAVAILABLE") return { status: "UNAVAILABLE", reason: selection.error?.code === "RUNNER_UNAVAILABLE"
          ? "Go 执行程序不可用，暂时无法打开系统目录窗口。原目录保持不变。"
          : errorMessage(selection.error, "运行主机的原生目录选择不可用。请确认使用同机浏览器和可交互桌面。") };
        if (selection.status === "SELECTED") {
          if (signal.aborted) return { status: "CANCELLED" }; // Late selection never overwrites a cancelled draft.
          if (!selection.targetId) throw Error("目录选择缺少受控目标，请重新选择。");
          const target = await client.get<DownloadTarget>("/download-targets/" + encodeURIComponent(selection.targetId));
          if (signal.aborted) return { status: "CANCELLED" };
          if (target.status !== "VALID") throw Error("所选目录尚未通过运行主机校验，请重新选择。");
          return { status: "SELECTED", path: target.path, reference: target.id, target };
        }
        await wait(2000);
      }
      if (!cancelRequested) await client.post(path + "/cancel", {});
      throw Error("目录选择等待超时，已请求关闭窗口；原目录保持不变。");
    },
    async validate(downloadTarget: Pick<DownloadTarget, "id" | "revision">, archiveDestination: DriveTarget | null): Promise<SettingsValidation> {
      const accepted = await client.post<{ validationId: string }>("/settings/validate", {
        downloadTarget: { id: downloadTarget.id, revision: downloadTarget.revision },
        archiveDestination: archiveDestination ? { accountId: archiveDestination.accountId, folderId: archiveDestination.folderId, revision: archiveDestination.revision } : null,
      });
      for (let attempt = 0; attempt < 60; attempt++) {
        const result = await client.get<SettingsValidation>("/settings/validations/" + encodeURIComponent(accepted.validationId));
        if (result.status !== "VALIDATING") return result;
        await wait(2000);
      }
      throw Error("设置校验仍未完成，请稍后重新校验；当前设置未修改。");
    },
    save: (validationId: string, revision: string) => client.patch<StorageSettings>("/settings", { validationId }, revision),
  };
}
export const archiveSettingsApi = createArchiveSettingsApi(api);
