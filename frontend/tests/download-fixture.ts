import type { ManagedChild, ManagedProject } from "../src/api/download-management";
import type { Transport } from "../src/api/types";

function child(projectId: string, id: string, title: string, platform: string, kind = "FILE", status = "RUNNING"): ManagedChild {
  const outputs = [{ id: id + "-out", relativePath: platform + "/" + title, kind, sizeBytes: "42", trackIds: kind === "MUXED" ? ["video", "audio"] : [] }];
  if (platform === "TELEGRAM") outputs.push({ id: id + "-metadata", relativePath: platform + "/" + title + ".metadata.json", kind: "METADATA", sizeBytes: "20", trackIds: [] });
  return { id, projectId, contentId: "content-" + projectId, partId: platform === "BILIBILI" ? id : null,
    attachmentId: platform === "TELEGRAM" ? id : null, nodeId: platform === "QUARK" ? id : null, title,
    revision: "1", status, desiredAction: "CONTINUE", allowedActions: status === "SUCCEEDED" ? [] : ["CANCEL"], actionReason: null,
    taskIds: [id + "-task"], outputs, files: status === "SUCCEEDED" ? outputs.map(output => ({ outputId: output.id, relativePath: output.relativePath, sizeBytes: output.sizeBytes!, verified: true })) : [] };
}
export function downloadFixtures(): ManagedProject[] {
  const project = (id: string, platform: ManagedProject["platform"], contentId: string, title: string, items: ManagedChild[], batchId = "batch-" + id): ManagedProject => {
    const result: ManagedProject = { id, batchId, platform, contentId, title, createdAt: "2026-10-15T08:00:00Z", revision: "1", status: "RUNNING", desiredAction: "CONTINUE", allowedActions: ["CANCEL"], actionReason: null,
      target: { id: "default", revision: "1", path: "D:\\Downloads", archivePolicy: "AUTO" }, archivePolicy: "AUTO", archiveTarget: null,
      summary: { total: items.length, completed: 0, running: 0, failed: 0, cancelled: 0, pending: 0 }, items };
    items.forEach(item => item.contentId = contentId); summarize(result); return result;
  };
  return [
    project("bil-new", "BILIBILI", "b1", "同名视频：设计的秩序", [child("bil-new", "p1", "P1 音视频.mp4", "BILIBILI", "MUXED"), child("bil-new", "p2", "P2 成品.mp4", "BILIBILI", "MUXED", "SUCCEEDED")]),
    project("bil-old", "BILIBILI", "b1", "同名视频：设计的秩序", [child("bil-old", "p1", "P1 旧批次.mp4", "BILIBILI", "MUXED")]),
    project("bil-other", "BILIBILI", "b2", "同名视频：设计的秩序", [child("bil-other", "p1", "另一个视频.mp4", "BILIBILI", "MUXED")]),
    project("tg-mixed", "TELEGRAM", "saved-group-87", "周末采集：视频、音频与照片", [child("tg-mixed", "tg-video", "片段.mp4", "TELEGRAM", "VIDEO"), child("tg-mixed", "tg-audio", "环境声.wav", "TELEGRAM", "AUDIO"), child("tg-mixed", "tg-photo", "现场照片.jpg", "TELEGRAM", "PHOTO")], "saved-scan-1"),
    project("tg-other", "TELEGRAM", "saved-message-92", "同次收藏增量中的另一条消息", [child("tg-other", "other-video", "其他片段.mp4", "TELEGRAM", "VIDEO")], "saved-scan-1"),
    project("quark", "QUARK", "share-12", "摄影课程分享", [child("quark", "q1", "课程一.mp4", "QUARK"), child("quark", "q2", "课程二.mp4", "QUARK")]),
  ];
}
function summarize(project: ManagedProject) {
  const items = project.items;
  const completed = items.filter(i => i.status === "SUCCEEDED").length, cancelled = items.filter(i => i.status === "CANCELLED").length, failed = items.filter(i => i.status === "FAILED").length;
  const active = items.filter(i => !["SUCCEEDED", "CANCELLED", "FAILED"].includes(i.status));
  project.summary = { total: items.length, completed, cancelled, failed, running: active.length, pending: 0 };
  project.status = active.length ? "RUNNING" : completed === items.length ? "SUCCEEDED" : cancelled === items.length ? "CANCELLED" : "PARTIAL";
  project.allowedActions = active.some(i => i.desiredAction !== "CANCEL") ? ["CANCEL"] : [];
}
/** Test-only controllable executor. No clock ever produces a fake completion. */
export function createDownloadFixture(saved?: ManagedProject[], persist: (value: ManagedProject[]) => void = () => {}) {
  const projects = saved ?? downloadFixtures();
  const requests: { method: string; path: string; headers: Record<string, string> }[] = [];
  let failure: "error" | "race" | null = null;
  const save = () => persist(structuredClone(projects));
  const finish = (projectId: string, itemId: string, status: string) => {
    const project = projects.find(p => p.id === projectId)!;
    const item = project.items.find(i => i.id === itemId)!;
    item.status = status; item.allowedActions = []; item.revision = String(Number(item.revision) + 1);
    if (status === "SUCCEEDED") item.files = item.outputs.map(o => ({ outputId: o.id, relativePath: o.relativePath, sizeBytes: o.sizeBytes ?? "0", verified: true }));
    project.revision = String(Number(project.revision) + 1); summarize(project); save();
  };
  const transport: Transport = { async request<T>(method: string, raw: string, _body?: unknown, headers: Record<string, string> = {}) {
    requests.push({ method, path: raw, headers });
    const url = new URL(raw, "http://fixture.invalid"), path = url.pathname;
    if (method === "GET" && path === "/download-projects") {
      const rows = projects.filter(p => (!url.searchParams.get("platform") || p.platform === url.searchParams.get("platform")) && (!url.searchParams.get("contentId") || p.contentId === url.searchParams.get("contentId")));
      const offset = Number(url.searchParams.get("cursor") ?? "0"), count = 2;
      return structuredClone({ items: rows.slice(offset, offset + count), total: rows.length, nextCursor: offset + count < rows.length ? String(offset + count) : null }) as T;
    }
    const segments = path.split("/").map(decodeURIComponent);
    const project = projects.find(p => p.id === segments[2]);
    if (!project) throw new Error("实例不存在");
    if (method === "GET") return structuredClone(project) as T;
    if (failure === "error") { failure = null; throw new Error("连接中断，取消未能确认"); }
    if (failure === "race") { failure = null; finish(project.id, project.items[0]!.id, "SUCCEEDED"); throw new Error("VERSION_CHANGED：文件刚刚完成"); }
    const target = segments[3] === "items" ? project.items.find(i => i.id === segments[4])! : project;
    if (headers["If-Match"] !== '"' + target.revision + '"') throw new Error("VERSION_CHANGED：请刷新");
    if (!headers["Idempotency-Key"]) throw new Error("缺少幂等键");
    target.desiredAction = "CANCEL"; target.allowedActions = []; target.revision = String(Number(target.revision) + 1);
    if (target === project) for (const item of project.items) if (!["SUCCEEDED", "CANCELLED", "FAILED"].includes(item.status)) { item.desiredAction = "CANCEL"; item.allowedActions = []; }
    save(); return structuredClone(target) as T;
  } };
  return { transport, projects, requests, finish, failNext(value: "error" | "race") { failure = value; } };
}
