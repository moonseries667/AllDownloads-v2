// Contract-shaped browser fixtures. Control intent persists; timers never finish I/O.
import type { ManagedChild, ManagedProject, ManagedOutput } from "./download-management";
import type { RecordData } from "./types";
import { defaultDemoTarget } from "./demo-bilibili";
import type { createManagementSeed } from "./demo-archive-settings";
const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value));
const terminal = (state: string) => ["SUCCEEDED", "FAILED", "CANCELLED"].includes(state);
const fail = (code: string, message: string): never => { throw Object.assign(Error(message), { code }); };
interface State { orders: RecordData[]; contents: RecordData[]; parts: RecordData[]; messages: RecordData[]; shares: RecordData[]; nodes: RecordData[]; settings: RecordData; scenario: string; management: ReturnType<typeof createManagementSeed> }
function summarize(project: ManagedProject) {
  const count = (states: string[]) => project.items.filter(item => states.includes(item.status)).length;
  project.summary = { total: project.items.length, completed: count(["SUCCEEDED"]), cancelled: count(["CANCELLED"]), failed: count(["FAILED", "NEEDS_ACTION"]), pending: count(["QUEUED", "PAUSED", "WAITING_RETRY"]), running: count(["RUNNING", "PAUSING"]) };
  const active = project.items.some(item => ["QUEUED", "RUNNING", "PAUSING", "PAUSED", "WAITING_RETRY"].includes(item.status));
  project.status = active ? (project.summary.running ? "RUNNING" : "QUEUED") : project.summary.completed === project.summary.total ? "SUCCEEDED" : project.summary.cancelled === project.summary.total ? "CANCELLED" : "PARTIAL";
  project.allowedActions = project.items.some(item => item.allowedActions.includes("CANCEL") && item.desiredAction !== "CANCEL") ? ["CANCEL"] : [];
}
function child(project: ManagedProject, id: string, title: string, outputs: ManagedOutput[], status: string, identity: Partial<ManagedChild> = {}): ManagedChild {
  return { id, projectId: project.id, contentId: project.contentId, partId: null, nodeId: null, attachmentId: null, title, outputs: clone(outputs),
    revision: "1", status, desiredAction: "CONTINUE", allowedActions: terminal(status) || status === "NEEDS_ACTION" ? [] : ["CANCEL"], actionReason: terminal(status) ? "当前部分已结束，文件保留。" : null,
    files: status === "SUCCEEDED" ? outputs.map(output => ({ outputId: output.id, relativePath: output.relativePath, sizeBytes: output.sizeBytes ?? "0", verified: true })) : [], taskIds: [], ...identity };
}
function project(state: State, id: string, batchId: string, platform: ManagedProject["platform"], contentId: string, title: string, target = defaultDemoTarget(state.settings)): ManagedProject {
  return { id, batchId, platform, contentId, title, target: clone(target), archivePolicy: target.archivePolicy, archiveTarget: target.archivePolicy === "AUTO" ? clone(state.management.archiveDestination) : null,
    createdAt: "2026-10-15T08:30:00Z", revision: "1", status: "QUEUED", desiredAction: "CONTINUE", allowedActions: ["CANCEL"], actionReason: null,
    items: [], summary: { total: 0, completed: 0, running: 0, failed: 0, cancelled: 0, pending: 0 } };
}
export function ensureDemoProjects(state: State) {
  if (state.management.projectsSeeded) return;
  state.management.projectsSeeded = true;
  if (state.scenario === "empty") return;
  const bilibili = project(state, "demo-bilibili-b2", "o1", "BILIBILI", "b2", state.contents.find(c => c.id === "b2")!.title);
  bilibili.items = state.parts.filter(part => part.contentId === "b2").map((part, index) => child(bilibili, "b2-" + part.id, part.title, [{ id: part.id + "-media", relativePath: part.demoDirectory + "/media.mp4", kind: "MUXED", sizeBytes: "100", trackIds: [part.id + "-video", part.id + "-audio"] }], index === 0 ? "SUCCEEDED" : "RUNNING", { partId: part.id }));
  const message = state.messages.find(message => message.id === "m1")!;
  const telegram = project(state, "demo-telegram-m1", "saved-scan-1", "TELEGRAM", "m1", "周末山间录音与拍摄笔记");
  telegram.items = message.attachments.map((attachment: RecordData, index: number) => child(telegram, "m1-" + attachment.id, attachment.name, [
    { id: attachment.id + "-media", relativePath: attachment.relativePath || "Telegram/demo-group/" + attachment.name, kind: attachment.mediaType === "PHOTO" ? "PHOTO" : "FILE", sizeBytes: attachment.sizeBytes },
    { id: attachment.id + "-metadata", relativePath: attachment.metadataRelativePath || "Telegram/demo-group/" + attachment.name + ".metadata.json", kind: "METADATA", sizeBytes: "100" },
  ], index === 0 ? "SUCCEEDED" : "RUNNING", { attachmentId: attachment.id }));
  const quark = project(state, "demo-quark-q1", "quark-download-1", "QUARK", "q1", state.shares.find(share => share.id === "q1")!.title);
  quark.items = state.nodes.filter(node => node.parentId === "q1-folder").map(node => child(quark, "q1-" + node.id, node.name, [{ id: node.id + "-output", relativePath: "Quark/12345678/" + node.relativePath, kind: "FILE", sizeBytes: node.sizeBytes }], "RUNNING", { nodeId: node.id }));
  state.management.projects.push(...[bilibili, telegram, quark].map(item => { summarize(item); return item; }));
}
export function addDemoProjects(state: State, order: RecordData, preview: RecordData) {
  ensureDemoProjects(state);
  const groups = new Map<string, RecordData[]>();
  for (const item of preview.items) { const key = preview.platform === "QUARK" ? item.shareId : item.contentId; groups.set(key, [...(groups.get(key) ?? []), item]); }
  const created: ManagedProject[] = [];
  for (const [contentId, items] of groups) {
    const id = order.id + ":" + contentId;
    const title = preview.platform === "QUARK" ? state.shares.find(share => share.id === contentId)?.title : state.contents.find(content => content.id === contentId)?.title;
    const value = project(state, id, order.id, preview.platform, contentId, title || items[0]!.title, preview.target);
    value.createdAt = order.createdAt;
    value.items = items.map(item => child(value, id + ":" + item.id, item.title, item.outputs || [],
      item.decision === "BLOCKED" ? "NEEDS_ACTION" : item.decision === "REUSE" ? ["LOCAL_VERIFIED", "ARCHIVE_VERIFIED"].includes(item.reasonCode) ? "SUCCEEDED" : "RUNNING" : "QUEUED",
      { partId: item.partId ?? null, nodeId: item.nodeId ?? null, attachmentId: item.attachmentId ?? null }));
    summarize(value); created.push(value);
  }
  state.management.projects.unshift(...created);
  return created.map(item => item.id);
}
export function handleDownloadProjectsDemo(state: State, method: string, path: string, body: RecordData, headers: Record<string, string>, query: URLSearchParams): { data: unknown } | null {
  if (!path.startsWith("/download-projects")) return null;
  ensureDemoProjects(state);
  const projects = state.management.projects;
  if (method === "GET" && path === "/download-projects") {
    const items = projects.filter(item => (!query.get("platform") || item.platform === query.get("platform")) && (!query.get("contentId") || item.contentId === query.get("contentId")));
    const offset = Number(query.get("cursor") || 0), size = Number(query.get("pageSize") || 50);
    return { data: { items: items.slice(offset, offset + size), total: items.length, nextCursor: offset + size < items.length ? String(offset + size) : null } };
  }
  const segments = path.split("/").map(decodeURIComponent), value = projects.find(item => item.id === segments[2]) ?? fail("NOT_FOUND", "下载实例不存在");
  if (method === "GET") return { data: value };
  if (method !== "POST" || !path.endsWith("/actions") || body.action !== "CANCEL") fail("ACTION_NOT_ALLOWED", "当前操作不受支持");
  const target = segments[3] === "items" ? value.items.find(item => item.id === segments[4]) ?? fail("NOT_FOUND", "下载子项不存在") : value;
  if (headers["If-Match"]?.replaceAll('"', "") !== target.revision) fail("VERSION_CHANGED", "任务状态已变化，请刷新后重试");
  if (target.desiredAction === "CANCEL" || terminal(target.status)) return { data: target };
  if (!target.allowedActions.includes("CANCEL")) fail("ACTION_NOT_ALLOWED", "当前状态不允许取消");
  const children = target === value ? value.items : [target as ManagedChild];
  for (const item of children) if (!terminal(item.status) && item.allowedActions.includes("CANCEL")) {
    item.desiredAction = "CANCEL"; item.allowedActions = []; item.revision = String(Number(item.revision) + 1);
    if (item.status === "QUEUED") item.status = "CANCELLED";
    item.actionReason = item.status === "CANCELLED" ? "尚未开始的演示子项已取消。" : "取消意图已保存；演示环境没有真实执行者确认停止。";
  }
  if (target === value) value.desiredAction = "CANCEL";
  value.revision = String(Number(value.revision) + 1); summarize(value);
  return { data: target };
}
