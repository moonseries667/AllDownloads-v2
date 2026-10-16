// Isolated synthetic responses for the committed part-three contract. No native or cloud I/O.
import type { ArchiveUpload, DirectorySelection, DriveFolder, DriveTarget, SettingsValidation } from "./management.generated";
import type { DownloadTarget, RecordData } from "./types";
import { defaultDemoTarget } from "./demo-bilibili";
import type { ManagedProject } from "./download-management";
const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value));
const fail = (code: string, message: string): never => { throw Object.assign(Error(message), { code }); };
function folder(id: string, parentId: string | null, name: string, path: string, selectable = true): DriveFolder {
  return { id, parentId, name, path, selectable, accountId: "g-main", kind: "FOLDER", revision: "1", availability: "AVAILABLE", availabilityReason: selectable ? null : "此目录没有上传权限" };
}
export const driveFolderFixtures: DriveFolder[] = [
  folder("drive-root", null, "我的云端硬盘", "我的云端硬盘"),
  folder("drive-archive", "drive-root", "AllDownloads", "我的云端硬盘/AllDownloads"),
  folder("drive-learning", "drive-root", "学习", "我的云端硬盘/学习"),
  folder("drive-courses-a", "drive-archive", "课程", "我的云端硬盘/AllDownloads/课程"),
  folder("drive-courses-b", "drive-learning", "课程", "我的云端硬盘/学习/课程"),
  folder("drive-readonly", "drive-root", "只读资料", "我的云端硬盘/只读资料", false),
];
export function driveTarget(folder: DriveFolder): DriveTarget {
  return { accountId: folder.accountId, folderId: folder.id, revision: folder.revision, name: folder.name, path: folder.path };
}
export function createManagementSeed() {
  const target = driveTarget(driveFolderFixtures[1]!);
  const uploads: ArchiveUpload[] = [
    ["archive-failed", "设计的秩序：网格与排版", "BILIBILI", "NEEDS_ACTION"],
    ["archive-verified", "手冲咖啡的日常", "BILIBILI", "SUCCEEDED"],
    ["archive-active", "摄影课程（完整下载示例）", "QUARK", "RUNNING"],
    ["archive-queued", "完整收藏消息示例", "TELEGRAM", "QUEUED"],
  ].map(([id, title, platform, status]) => ({
    id: id!, title: title!, platform: platform!, status: status as ArchiveUpload["status"], projectId: "project-" + id, batchId: "batch-" + id,
    revision: "1", desiredAction: "CONTINUE", allowedActions: status === "NEEDS_ACTION" ? ["RETRY"] : status === "RUNNING" ? ["PAUSE"] : [],
    progress: { doneBytes: status === "RUNNING" || status === "SUCCEEDED" ? "1048576" : "0", totalBytes: "1048576", doneItems: status === "SUCCEEDED" ? 1 : 0, totalItems: 1, bytesPerSecond: null },
    target: clone(target), files: [{ outputId: id + "-file", relativePath: "demo/example.mp4", verified: true }], taskId: "task-" + id,
    createdAt: "2026-10-15T08:30:00Z", error: status === "NEEDS_ACTION" ? { code: "AUTH_REQUIRED", message: "Google Drive 需要重新验证账号" } : null,
  }));
  return { uploads, projects: [] as ManagedProject[], projectsSeeded: false, archiveDestination: target as DriveTarget | null, selections: {} as Record<string, DirectorySelection>, validations: {} as Record<string, SettingsValidation> };
}
interface State {
  settings: RecordData; scenario: string; targets: Record<string, DownloadTarget>; management: ReturnType<typeof createManagementSeed>;
}
export function handleManagementDemo(state: State, method: string, path: string, body: RecordData, headers: Record<string, string>, query: URLSearchParams): { data: unknown } | null {
  const m = state.management;
  const settings = () => ({ ...state.settings, defaultTarget: defaultDemoTarget(state.settings), archiveDestination: clone(m.archiveDestination), directoryPicker: { available: false, reasonCode: "DEMO_ONLY", hostId: "demo-host" } });
  const revision = (actual: string) => { if (headers["If-Match"]?.replaceAll('"', "") !== actual) fail("VERSION_CHANGED", "版本已变化，请重新读取后操作"); };
  if (path === "/archives" && method === "GET") {
    const items = state.scenario === "empty" ? [] : m.uploads.filter(item => (!query.get("platform") || item.platform === query.get("platform")) && (!query.get("status") || item.status === query.get("status")));
    const offset = Number(query.get("cursor") || 0), size = Number(query.get("pageSize") || 50);
    return { data: { items: items.slice(offset, offset + size), total: items.length, nextCursor: offset + size < items.length ? String(offset + size) : null } };
  }
  if (path.startsWith("/tasks/")) {
    const upload = m.uploads.find(item => item.taskId === path.split("/")[2]);
    if (upload && method === "POST" && path.endsWith("/actions")) {
      revision(upload.revision);
      if (!upload.allowedActions.includes(body.action)) fail("ACTION_NOT_ALLOWED", "当前上传不支持该操作");
      if (body.action === "PAUSE") { upload.desiredAction = "PAUSE"; upload.status = "PAUSING"; upload.allowedActions = []; }
      else { upload.status = "QUEUED"; upload.desiredAction = "CONTINUE"; upload.allowedActions = []; upload.error = null; }
      upload.revision = String(Number(upload.revision) + 1);
      return { data: clone(upload) };
    }
  }
  if (path === "/settings" && method === "GET") return { data: settings() };
  if (path === "/directory-selections" && method === "POST") {
    const id = crypto.randomUUID();
    m.selections[id] = { id, status: "UNAVAILABLE", targetId: null, error: { code: "NATIVE_PICKER_UNAVAILABLE", message: "演示模式不打开系统窗口；真实目录选择需要运行主机的原生交互能力。" } };
    return { data: { selectionId: id, taskId: null } };
  }
  if (path.startsWith("/directory-selections/")) {
    const value = m.selections[path.split("/")[2]!] ?? fail("NOT_FOUND", "选择请求不存在");
    if (method === "POST" && path.endsWith("/cancel") && value.status === "PENDING") value.status = "CANCELLED";
    return { data: value };
  }
  if (path === "/drive/folders" && method === "GET") {
    if (state.scenario === "login") fail("AUTH_REQUIRED", "请先完成 Google Drive 登录");
    if (query.get("accountId") !== "g-main") fail("ACCOUNT_PERMISSION_DENIED", "该账号不是已配置的 Google Drive 账号");
    const parent = driveFolderFixtures.find(folder => folder.id === (query.get("parentId") || "drive-root")) ?? fail("TARGET_NOT_FOUND", "目录已失效，请返回根目录重新选择");
    const breadcrumbs: DriveFolder[] = [];
    let node: DriveFolder | undefined = parent;
    while (node) { breadcrumbs.unshift(node); const parentId: string | null = node.parentId; node = driveFolderFixtures.find(folder => folder.id === parentId); }
    const items = driveFolderFixtures.filter(folder => folder.parentId === parent.id), size = Number(query.get("pageSize") || 50), offset = Number(query.get("cursor") || 0);
    return { data: { parent, breadcrumbs, items: items.slice(offset, offset + size), total: items.length, nextCursor: offset + size < items.length ? String(offset + size) : null, status: "READY", taskId: null, error: null } };
  }
  if (path === "/settings/validate" && method === "POST") {
    const defaultTarget = defaultDemoTarget(state.settings);
    const target = body.downloadTarget?.id === defaultTarget.id ? defaultTarget : state.targets[body.downloadTarget?.id];
    const folder = body.archiveDestination && driveFolderFixtures.find(folder => folder.id === body.archiveDestination.folderId && folder.accountId === body.archiveDestination.accountId);
    let error: RecordData | null = null;
    if (!target || target.status !== "VALID" || target.revision !== body.downloadTarget?.revision) error = { code: "INVALID_STORAGE_ROOT", message: "本机目录引用无效，请重新选择" };
    else if (body.archiveDestination && !folder) error = { code: "TARGET_NOT_FOUND", message: "Google Drive 目录已失效，请重新选择" };
    else if (folder && (!folder.selectable || folder.revision !== body.archiveDestination.revision)) error = { code: "TARGET_CHANGED", message: "Google Drive 目录或权限已变化，请重新选择" };
    const id = crypto.randomUUID();
    m.validations[id] = { id, revision: state.settings.revision, status: error ? "INVALID" : "VALID", downloadTarget: clone(target ?? {}), archiveDestination: folder ? driveTarget(folder) : null, error };
    return { data: { validationId: id, taskId: null } };
  }
  if (path.startsWith("/settings/validations/") && method === "GET") return { data: m.validations[path.split("/")[3]!] ?? fail("NOT_FOUND", "设置校验不存在") };
  if (path === "/settings" && method === "PATCH") {
    revision(state.settings.revision);
    const validation = m.validations[body.validationId] ?? fail("INVALID_INPUT", "请先校验设置");
    if (validation.status !== "VALID") fail("INVALID_INPUT", "设置校验尚未通过");
    if (validation.revision !== state.settings.revision) fail("VERSION_CHANGED", "校验基于旧设置，请重新校验");
    state.settings.downloadRoot = validation.downloadTarget.path;
    state.settings.revision = String(Number(state.settings.revision) + 1);
    state.settings.storageRevision = String(Number(state.settings.storageRevision) + 1);
    m.archiveDestination = clone(validation.archiveDestination);
    state.settings.archiveDestinationLabel = m.archiveDestination?.path ?? "尚未选择";
    return { data: settings() };
  }
  return null;
}
