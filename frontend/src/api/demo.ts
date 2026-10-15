import { createManagementSeed, handleManagementDemo } from "./demo-archive-settings";
import { ensureDemoProjects, addDemoProjects, handleDownloadProjectsDemo } from "./demo-download-projects";
import { validateTelegramLinks } from "./telegram-links";
export { validateTelegramLinks } from "./telegram-links";
import { createSeed, type DemoState } from "./fixtures";
import type { RecordData, Selection, Transport } from "./types";
import { defaultDemoTarget, demoBilibiliPreview, demoContent, demoSource, filterDemoContents, sortedContentPage } from "./demo-bilibili";
import { demoQuarkFolderPage, validateDemoQuarkTarget } from "./quark-folder-fixtures";
import { demoQuarkPreview } from "./demo-quark";
const key = "alldownloads.demo.feedback2.v3";
const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value));
const fail = (code: string, message: string): never => {
  throw Object.assign(new Error(message), { code });
};
export function createDemoTransport(
  options: { persist?: boolean; state?: DemoState } = {},
): Transport & { inspect: () => DemoState } {
  let state = options.state ?? createSeed();
  const persist =
    options.persist !== false && typeof localStorage !== "undefined";
  if (persist) {
    try {
      const saved = JSON.parse(localStorage.getItem(key) || "null");
      if (saved?.version === 3) state = { ...saved, management: saved.management ?? createManagementSeed() };
    } catch {
      /* A corrupt demo cache is replaced with isolated fixtures. */
    }
  }
  const receipts = new Map<string, { body: string; result: unknown }>();
  state.management.projects ??= [];
  ensureDemoProjects(state);
  const save = () => {
    if (persist) localStorage.setItem(key, JSON.stringify(state));
  };
  const id = (prefix: string) => prefix + "-" + crypto.randomUUID().slice(0, 8);
  const now = () => new Date().toISOString();
  const list = (items: RecordData[], p: URLSearchParams) => {
    const n = Number(p.get("pageSize") || 200),
      offset = Number(p.get("cursor") || 0);
    return {
      items: items.slice(offset, offset + n),
      total: items.length,
      nextCursor: offset + n < items.length ? String(offset + n) : null,
    };
  };
  const task = (
    title: string,
    platform: string,
    kind: string,
    phase: string,
  ) => {
    const t = {
      id: id("task"),
      title,
      platform,
      kind,
      status: "QUEUED",
      phase,
      progress: {
        doneItems: 0,
        totalItems: null,
        doneBytes: "0",
        totalBytes: null,
        bytesPerSecond: null,
      },
      desiredAction: "CONTINUE",
      allowedActions:
        platform === "TELEGRAM" ? ["CANCEL"] : ["PAUSE", "CANCEL"],
      error: null,
      nextRetryAt: null,
      createdAt: now(),
      updatedAt: now(),
      revision: "1",
      completedSteps: ["请求已受理"],
      remainingSteps: ["等待执行程序", "核对成果"],
    };
    state.tasks.unshift(t);
    return t;
  };
  const find = (items: RecordData[], id: string) =>
    items.find((x) => x.id === id) ??
    fail("NOT_FOUND", "记录不存在，请刷新列表");
  const checkRevision = (item: RecordData, headers: Record<string, string>) => {
    if (headers["If-Match"] && headers["If-Match"].replace(/^"|"$/g, "") !== item.revision)
      fail("VERSION_CHANGED", "记录已更新，请刷新后重试");
  };
  const writable = () => {
    if (state.scenario === "offline")
      fail("CONNECTION_LOST", "连接中断，已保留当前内容与选择");
    if (state.scenario === "runner")
      fail("RUNNER_UNAVAILABLE", "执行程序不可用，仍可浏览和选择");
    if (state.scenario === "login")
      fail("AUTH_REQUIRED", "账号需要重新登录，已保留选择");
  };
  function filteredContents(filters: RecordData) {
    return filterDemoContents(state.contents, filters);
  }
  function preview(body: RecordData) {
    if (body.platform === "TELEGRAM" || body.selection?.resource === "TELEGRAM_ATTACHMENTS") fail("FEATURE_REMOVED", "Telegram 仅支持收藏夹增量下载");
    const selection = body.selection as Selection;
    if (!selection) fail("EMPTY_SELECTION", "请选择内容");
    let entries: RecordData[] = [];
    let ids = selection.ids ?? [];
    if (selection.mode === "QUERY" && body.platform === "BILIBILI")
      ids = filteredContents(selection.filters ?? {})
        .filter((c) => !selection.excludeIds?.includes(c.id))
        .map((c) => c.id);
    if (selection.mode === "EXPLICIT" && !ids.length) fail("EMPTY_SELECTION", "请选择至少一项");
    const target = body.target ? state.targets[body.target.id] : defaultDemoTarget(state.settings);
    if (!target || target.status !== "VALID") fail("INVALID_STORAGE_ROOT", "目标目录尚未通过校验");
    if (body.target && body.target.revision !== target.revision) fail("VERSION_CHANGED", "目标目录已变化，请重新校验");
    if (body.platform === "BILIBILI") {
      entries = demoBilibiliPreview(state.contents, state.parts, selection, ids, target);
    } else if (body.platform === "QUARK") {
      entries = demoQuarkPreview(state.nodes, state.shares, selection, target);
    }
    if (!entries.length) fail("EMPTY_SELECTION", "所选范围没有可处理文件");
    const p = {
      id: id("preview"),
      status: "READY",
      platform: body.platform,
      expiresAt: new Date(Date.now() + 600000).toISOString(),
      storageRevision: state.settings.storageRevision,
      target: clone(target),
      archivePolicy: target.archivePolicy,
      items: entries,
      summary: {
        download: entries.filter((x) => x.decision === "DOWNLOAD").length,
        reuse: entries.filter((x) => x.decision === "REUSE").length,
        blocked: entries.filter((x) => x.decision === "BLOCKED").length,
        total: entries.length,
      },
      warnings: [
        "演示清单不代表真实平台结果，也未校验实际磁盘。",
      ],
    };
    state.previews[p.id] = p;
    return { previewId: p.id, taskId: id("preview-task") };
  }
  async function handle(
    method: string,
    raw: string,
    body: RecordData = {},
    headers: Record<string, string> = {},
  ): Promise<any> {
    const url = new URL(raw, "http://demo.local"),
      path = url.pathname,
      p = url.searchParams;
    if (path === "/__demo/scenario" && method === "POST") {
      state = createSeed();
      state.scenario = body.scenario;
      if (body.scenario === "empty") {
        state.contents = [];
        state.parts = [];
        state.sources = [];
        state.messages = [];
        state.resources = [];
        state.shares = [];
        state.nodes = [];
        state.tasks = [];
        state.orders = [];
        state.artifacts = [];
      }
      if (body.scenario === "scanned") {
        state.watcher.scanState.initialScanCompleted = true;
        state.watcher.scanState.cursorMessageId = "1003";
        state.watcher.scanState.scannedThroughAt = "2026-10-14T11:05:00Z";
      }
      return { scenario: state.scenario };
    }
    if (path === "/__demo/status") return { scenario: state.scenario };
    if (method !== "GET" && !path.startsWith("/auth-sessions")) writable();
    const management = handleManagementDemo(state, method, path, body, headers, p);
    if (management) return management.data;
    const downloads = handleDownloadProjectsDemo(state, method, path, body, headers, p);
    if (downloads) return downloads.data;
    if (path === "/download-targets/validate" && method === "POST") {
      const targetId = id("target");
      const valid = typeof body.path === "string" && /^[A-Za-z]:[\\/]/.test(body.path) &&
        !body.path.startsWith("\\\\") && !body.path.split(/[\\/]/).includes("..");
      state.targets[targetId] = { id: targetId, revision: "1", status: valid ? "VALID" : "INVALID",
        path: body.path, label: body.path, rootId: valid ? "demo-root-" + targetId : null,
        archivePolicy: "NONE", error: valid ? null : { code: "INVALID_STORAGE_ROOT", message: "请输入本机磁盘绝对目录，不能使用网络目录或上级跳转" } };
      return { targetId, taskId: "demo-validation-" + targetId };
    }
    if (path.startsWith("/download-targets/") && method === "GET")
      return state.targets[path.split("/")[2]] ?? fail("NOT_FOUND", "目录校验记录不存在");
    if (path === "/accounts") return list(state.accounts, p);
    if (path.match(/^\/accounts\/[^/]+\/check$/))
      return find(state.accounts, path.split("/")[2]);
    if (path === "/auth-sessions" && method === "POST") {
      const a = {
        authSessionId: id("auth"),
        nextAction: "DEMO_ONLY",
        expiresAt: new Date(Date.now() + 300000).toISOString(),
        state: "PENDING",
        message:
          "纯前端演示不建立真实登录。正式版本由 Java 返回官方二维码或授权步骤。",
      };
      state.authSessions[a.authSessionId] = a;
      return a;
    }
    if (path.startsWith("/auth-sessions/")) {
      const a = state.authSessions[path.split("/")[2]];
      if (!a) fail("NOT_FOUND", "登录会话不存在");
      if (method === "DELETE") a.state = "CANCELLED";
      return a;
    }
    const runtime = {
      state: "UNKNOWN", observedAt: now(),
      services: [
        { id: "java", required: true, state: "UNKNOWN", reasonCode: "DEMO_ONLY", lastSeenAt: null },
        { id: "go", required: true, state: state.scenario === "runner" ? "LOST" : "UNKNOWN", reasonCode: "DEMO_ONLY", lastSeenAt: null },
      ],
      mount: { state: "UNKNOWN", reasonCode: "DEMO_ONLY", observedAt: now() },
    };
    const bilibili = { accountState: state.scenario === "login" ? "AUTH_REQUIRED" : "UNKNOWN",
      refreshState: state.tasks.some((t) => t.platform === "BILIBILI" && t.kind === "INFO_SYNC" && ["RUNNING", "QUEUED"].includes(t.status)) ? "RUNNING" : "IDLE",
      downloadCount: state.contents.filter((c) => c.downloadSummary.state === "VERIFIED").length,
      archiveCount: state.contents.filter((c) => c.archiveSummary.state === "VERIFIED").length };
    if (path === "/health") return { runtime, bilibili };
    if (path === "/overview") return { runtime, bilibili };
    if (path === "/search") {
      const q = (p.get("q") || "").toLowerCase();
      return list(
        [
          ...state.contents.map((c) => ({
            id: c.id,
            title: c.title,
            platform: c.platform,
            url: "/bilibili?q=" + encodeURIComponent(c.title),
          })),
          ...state.messages.filter((message) => message.eligible === true).map((r) => ({
            id: r.id,
            title: r.text.split("\n")[0],
            platform: "TELEGRAM",
            url: "/telegram?q=" + encodeURIComponent(r.text.split("\n")[0]),
          })),
          ...state.nodes
            .filter((n) => n.kind === "FILE")
            .map((n) => ({
              id: n.id,
              title: n.name,
              platform: "QUARK",
              url: "/quark/cloud?q=" + encodeURIComponent(n.name),
            })),
        ].filter((x) => q && x.title.toLowerCase().includes(q)),
        p,
      );
    }
    if (path === "/bilibili/sources") {
      if (method === "GET") return list(state.sources.map(demoSource), p);
      return {
        batchId: id("batch"),
        items: body.links.map((link: string) => {
          let u: URL;
          try {
            u = new URL(link);
          } catch {
            return { url: link, error: { message: "无法识别链接" } };
          }
          if (
            !/(^|\.)bilibili\.com$/.test(u.hostname) &&
            u.hostname !== "b23.tv"
          )
            return { url: link, error: { message: "请使用 B 站来源链接" } };
          const exists = state.sources.find((s) => s.url === link);
          if (exists) return { url: link, sourceId: exists.id, reused: true };
          const kind = u.pathname.includes("bangumi")
            ? "SERIES"
            : u.pathname.includes("video")
              ? "USER_ADDED"
              : u.pathname.includes("favlist")
                ? "FAVORITE"
                : "COLLECTION";
          const s = {
            id: id("source"),
            platform: "BILIBILI",
            kind,
            title: "新加入的来源",
            alias: "",
            url: link,
            status: "QUEUED",
            lastSyncedAt: null,
            itemCount: 0,
            revision: "1",
            pinned: false,
          };
          state.sources.push(s as any);
          return {
            url: link,
            sourceId: s.id,
            reused: false,
            taskId: task("解析 B 站来源", "BILIBILI", "INFO_SYNC", "收录信息")
              .id,
          };
        }),
      };
    }
    if (path.match(/^\/bilibili\/sources\/[^/]+/)) {
      const s = find(state.sources, path.split("/")[3]);
      checkRevision(s, headers);
      if (path.endsWith("/refresh"))
        return {
          taskId: task(s.title, "BILIBILI", "INFO_SYNC", "刷新来源信息").id,
        };
      if (method === "PATCH") {
        if (state.scenario === "pin-failure") fail("VERSION_CHANGED", "来源设置已变化，请刷新后重试");
        Object.assign(s, body, { revision: String(Number(s.revision) + 1) });
        return demoSource(s);
      }
      if (method === "DELETE") {
        state.sources = state.sources.filter((x) => x.id !== s.id);
        state.contents.forEach(
          (c) => (c.sourceIds = c.sourceIds.filter((x: string) => x !== s.id)),
        );
        return { removed: true };
      }
      return demoSource(s);
    }
    if (path === "/bilibili/contents") {
      const revision = JSON.stringify([state.contents.map((c) => [c.id, c.revision]), state.sources.map((s) => [s.id, s.revision])]);
      const page = sortedContentPage(state.contents, p, revision);
      return { ...page, items: page.items.map((c) => demoContent(c, state.sources, state.parts)) };
    }
    if (path.match(/^\/bilibili\/contents\/[^/]+/)) {
      const c = find(state.contents, path.split("/")[3]);
      const content = demoContent(c, state.sources, state.parts);
      return path.endsWith("/parts") ? list(content.mediaUnits, p) : content;
    }
    if (path === "/telegram/messages" || path === "/telegram/resources") {
      const items = path.endsWith("messages")
        ? state.messages.filter((message) => message.eligible === true)
        : state.resources;
      return list(
        items.filter(
          (x) =>
            (!p.get("q") ||
              [
                x.text,
                x.channel,
                ...(x.authorTags ?? []),
                ...x.attachments.map((a: RecordData) => a.name),
              ]
                .filter(Boolean)
                .join(" ")
                .toLowerCase()
                .includes(p.get("q")!.toLowerCase())) &&
            (!p.get("state") || x.state === p.get("state")) &&
            (!p.get("mediaType") ||
              x.attachments.some(
                (a: RecordData) => a.mediaType === p.get("mediaType"),
              )),
        ),
        p,
      );
    }
    if (path.match(/^\/telegram\/resources\/[^/]+/)) {
      const r = find(state.resources, path.split("/")[3]);
      return path.endsWith("/metadata")
        ? {
            schemaVersion: "1",
            resource: r,
            messages: state.messages.filter((m) => r.messageIds.includes(m.id)),
            files: r.attachments
              .filter((a: RecordData) => a.downloadState === "VERIFIED")
              .flatMap((a: RecordData) => [
                {
                  attachmentId: a.id,
                  relativePath: a.relativePath,
                  metadataRelativePath: a.metadataRelativePath,
                  metadata: { messageId: a.messageId, name: a.name },
                },
              ]),
          }
        : r;
    }
    if (path.match(/^\/telegram\/messages\/[^/]+$/))
      return find(state.messages.filter((message) => message.eligible === true), path.split("/")[3]);
    if (path === "/telegram/watcher") {
      if (method === "PATCH") {
        if (body.enabled && !state.watcher.scanState.initialScanCompleted)
          fail("INITIAL_SCAN_REQUIRED", "请先完成首次收藏扫描");
        state.watcher.enabled = body.enabled;
        state.watcher.nextCheckAt = (
          body.enabled ? new Date(Date.now() + 60000).toISOString() : null
        ) as any;
      }
      return state.watcher;
    }
    if (path === "/telegram/saved-sync") {
      const active = state.tasks.find(
        (t) =>
          t.kind === "INFO_SYNC" &&
          t.platform === "TELEGRAM" &&
          ["RUNNING", "QUEUED"].includes(t.status),
      );
      const t =
        active ??
        task(
          "下载收藏增量",
          "TELEGRAM",
          "INFO_SYNC",
          state.watcher.scanState.initialScanCompleted
            ? "扫描游标后消息并补齐 pending"
            : "从消息 ID 1 扫描全部历史",
        );
      state.watcher.scanState.activeTaskId = t.id as any;
      return { taskId: t.id, reused: !!active };
    }
    if (path === "/telegram/link-downloads") fail("FEATURE_REMOVED", "Telegram 仅支持收藏夹增量下载");
    if (path === "/telegram/retry-incomplete")
      return {
        taskId: task("补齐未完成附件", "TELEGRAM", "DOWNLOAD", "仅补齐失败附件")
          .id,
      };
    if (path === "/quark/shares") {
      if (method === "GET") return list(state.shares, p);
      return {
        batchId: id("batch"),
        items: body.entries.map((entry: RecordData) => {
          let u: URL;
          try {
            u = new URL(entry.url);
          } catch {
            return { url: entry.url, error: { message: "无效分享链接" } };
          }
          const match = u.pathname.match(/^\/s\/([A-Za-z0-9]+)\/?$/);
          if (u.hostname !== "pan.quark.cn" || !match)
            return {
              url: entry.url,
              error: { message: "无法识别夸克分享链接" },
            };
          let target;
          try { target = validateDemoQuarkTarget(entry.targetFolderId, entry.targetFolderRevision); }
          catch (error) { return { url: entry.url, error: { code: (error as { code: string }).code, message: (error as Error).message } }; }
          const full = match[1], existing = state.shares.find((share) => share.externalShareId === full);
          if (existing) return { shareId: existing.id, reused: true };
          let len = Math.min(8, full.length),
            folder = full.slice(-len);
          while (
            state.shares.some(
              (s) => s.shareFolderKey.toLowerCase() === folder.toLowerCase(),
            ) &&
            len < full.length
          )
            folder = full.slice(-++len);
          if (
            state.shares.some(
              (s) => s.shareFolderKey.toLowerCase() === folder.toLowerCase(),
            )
          )
            return { error: { message: "分享目录键冲突，需协调处理" } };
          const share = {
            id: id("share"),
            accountId: "q-main",
            externalShareId: full,
            title: "新分享 " + folder,
            targetFolderId: entry.targetFolderId,
            targetFolderRevision: target.revision,
            targetPath: target.path,
            shareFolderKey: folder,
            watchEnabled: true,
            updateState: "QUEUED",
            lastCheckedAt: null,
            nextCheckAt: null,
            revision: "1",
          };
          state.shares.push(share);
          return {
            shareId: share.id,
            taskId: task(
              share.title,
              "QUARK",
              "TRANSFER_SYNC",
              "转存并维护更新",
            ).id,
          };
        }),
      };
    }
    if (path === "/quark/folders") return demoQuarkFolderPage(p);
    if (path === "/quark/nodes")
      return list(
        state.nodes.filter(
          (n) =>
            (!p.get("shareId") || n.shareId === p.get("shareId")) &&
            (!p.get("q") || n.name.includes(p.get("q"))),
        ),
        p,
      );
    if (path.startsWith("/quark/shares/")) {
      const s = find(state.shares, path.split("/")[3]);
      checkRevision(s, headers);
      if (method === "PATCH") {
        s.watchEnabled = body.watchEnabled;
        s.revision = String(Number(s.revision) + 1);
        return s;
      }
      if (path.endsWith("/refresh") || path.endsWith("/refresh-tree"))
        return {
          taskId: task(
            s.title,
            "QUARK",
            path.endsWith("refresh-tree") ? "INFO_SYNC" : "TRANSFER_SYNC",
            path.endsWith("refresh-tree")
              ? "刷新网盘目录"
              : "检查并转存新增内容",
          ).id,
        };
      return s;
    }
    if (path === "/download-previews" && method === "POST")
      return preview(body);
    if (path.startsWith("/download-previews/")) {
      const pv =
        state.previews[path.split("/")[2]] ?? fail("NOT_FOUND", "预览不存在");
      return path.endsWith("/items") ? list(pv.items, p) : pv;
    }
    if (path === "/download-orders") {
      if (method === "GET")
        return list(
          state.orders.filter(
            (o) =>
              (!p.get("platform") || o.platform === p.get("platform")) &&
              (!p.get("sourceId") || o.sourceId === p.get("sourceId")),
          ),
          p,
        );
      if (state.scenario === "disk")
        fail("INSUFFICIENT_SPACE", "本机剩余空间不足，请释放空间后重新提交");
      const pv =
        state.previews[body.previewId] ?? fail("NOT_FOUND", "预览不存在");
      if (pv.orderId) return { orderId: pv.orderId, reused: true, taskIds: [] };
      if (pv.status !== "READY" || Date.parse(pv.expiresAt) < Date.now())
        fail("VERSION_CHANGED", "预览已过期，请重新预览");
      if (pv.storageRevision !== state.settings.storageRevision)
        if (pv.archivePolicy !== "NONE") fail("STORAGE_CHANGED", "下载根版本已变化，请重新预览");
      if (pv.archivePolicy === "NONE" && (!state.targets[pv.target.id] || state.targets[pv.target.id].revision !== pv.target.revision || state.targets[pv.target.id].status !== "VALID"))
        fail("VERSION_CHANGED", "本批目录校验状态已变化，请重新校验并刷新");
      if (
        state.scenario === "version" ||
        pv.items.some(
          (i: RecordData) =>
            (i.nodeId && find(state.nodes, i.nodeId).version !== i.version) ||
            (i.contentId && find(state.contents, i.contentId).revision !== i.version),
        )
      )
        fail("VERSION_CHANGED", "所选文件版本已变化，请重新预览后确认");
      if (!pv.summary.download)
        fail("EMPTY_SELECTION", "没有需要下载的文件，已存在项将被复用");
      const t = task(
        "用户下载 · " + pv.summary.download + " 个文件",
        pv.platform,
        "DOWNLOAD",
        "等待下载",
      );
      const o = {
        id: id("order"),
        previewId: pv.id,
        target: clone(pv.target),
        archivePolicy: pv.archivePolicy,
        status: "QUEUED",
        title: pv.items[0].title,
        platform: pv.platform,
        sourceId: pv.items[0].contentId
          ? find(state.contents, pv.items[0].contentId).sourceIds[0]
          : null,
        state: "QUEUED",
        createdAt: now(),
        completedCount: 0,
        totalCount: pv.summary.download,
        taskIds: [t.id],
        items: clone(
          pv.items.filter((i: RecordData) => i.decision === "DOWNLOAD"),
        ),
      };
      state.orders.unshift(o);
      pv.orderId = o.id;
      const projectIds = addDemoProjects(state, o, pv);
      return { orderId: o.id, taskIds: [t.id], projectIds, reused: false };
    }
    if (path.startsWith("/download-orders/"))
      return find(state.orders, path.split("/")[2]);
    if (path === "/artifacts")
      return list(
        state.artifacts.filter(
          (a) => !p.get("platform") || a.platform === p.get("platform"),
        ),
        p,
      );
    if (path.startsWith("/artifacts/")) {
      const a = find(state.artifacts, path.split("/")[2]);
      if (method === "GET") return a;
      if (path.endsWith("/open-local"))
        return {
          opened: false,
          message:
            "演示环境不打开实际磁盘目录。登记位置：" + a.relativeDirectory,
        };
      if (path.endsWith("/archive")) {
        if (a.localState !== "VERIFIED")
          fail("INVALID_INPUT", "必要本机文件尚未完整，不能归档");
        return {
          taskId: task(a.title, a.platform, "UPLOAD", "仅上传已有本机成果").id,
        };
      }
      return {
        taskId: task(a.title, a.platform, "VERIFY", "核对已登记成果").id,
      };
    }
    if (path === "/cleanup-previews") {
      const items = state.artifacts.filter(
        (a) => a.localState === "VERIFIED" && a.archiveState === "VERIFIED",
      );
      const cp = {
        id: id("cleanup"),
        items,
        bytes: String(
          items
            .flatMap((a) => a.files)
            .reduce((n, f) => n + Number(f.sizeBytes), 0),
        ),
      };
      state.cleanups[cp.id] = cp;
      return cp;
    }
    if (path === "/cleanup-orders") {
      const cp =
        state.cleanups[body.previewId] ?? fail("NOT_FOUND", "清理预览不存在");
      for (const a of cp.items) {
        const live = find(state.artifacts, a.id);
        if (live.archiveState !== "VERIFIED")
          fail("VERSION_CHANGED", "云端验证状态发生变化，请重新预览");
        live.localState = "CLEANED";
      }
      return { cleaned: cp.items.length, simulated: true };
    }
    if (path === "/tasks") return list(state.tasks, p);
    if (path.startsWith("/tasks/")) {
      const t = find(state.tasks, path.split("/")[2]);
      if (method === "GET") return t;
      checkRevision(t, headers);
      if (!t.allowedActions.includes(body.action))
        fail("ACTION_NOT_ALLOWED", "当前任务不支持该操作");
      if (body.action === "PAUSE") {
        t.desiredAction = "PAUSE";
        t.status = "PAUSING";
        t.allowedActions = ["CANCEL"];
      }
      if (body.action === "CANCEL") {
        t.desiredAction = "CANCEL";
        t.allowedActions = [];
      }
      if (["RETRY", "RECONCILE", "RESUME"].includes(body.action)) {
        t.status = "QUEUED";
        t.desiredAction = "CONTINUE";
        t.allowedActions = ["CANCEL"];
      }
      t.revision = String(Number(t.revision) + 1);
      return t;
    }
    if (path === "/issues")
      return list(
        state.tasks.filter((task) => task.error && (p.get("includeResolved") === "true" || task.status !== "SUCCEEDED" || task.error.severity === "WARNING"))
          .map((task) => {
            const issueId = task.id + ":" + task.error.code;
            const read = state.acknowledged.includes(issueId);
            return {
              id: issueId, taskId: task.id, objectId: null, code: task.error.code,
              message: task.error.message, severity: task.error.severity ?? "ERROR",
              occurredAt: task.createdAt, updatedAt: task.updatedAt, read,
              resolved: task.status === "SUCCEEDED" && task.error.severity !== "WARNING",
              title: task.title, platform: task.platform, phase: task.phase, error: task.error, acknowledged: read,
            };
          }).sort((a, b) => b.occurredAt.localeCompare(a.occurredAt) || a.id.localeCompare(b.id)), p);
    if (path.match(/^\/issues\/[^/]+\/acknowledge$/)) {
      const issueId = decodeURIComponent(path.split("/")[2]);
      if (!state.tasks.some((task) => task.error && task.id + ":" + task.error.code === issueId)) fail("NOT_FOUND", "问题记录不存在");
      if (!state.acknowledged.includes(issueId)) state.acknowledged.push(issueId);
      return { read: true, acknowledged: true };
    }
    fail("NOT_IMPLEMENTED", "此演示接口尚未实现：" + method + " " + path);
  }
  return {
    inspect: () => clone(state),
    async request<T>(
      method: string,
      path: string,
      body?: unknown,
      headers: Record<string, string> = {},
    ): Promise<T> {
      const receiptKey = headers["Idempotency-Key"],
        serialized = JSON.stringify({ method, path, body });
      if (receiptKey && receipts.has(receiptKey)) {
        const r = receipts.get(receiptKey)!;
        if (r.body !== serialized)
          fail("IDEMPOTENCY_CONFLICT", "重复请求的内容发生变化");
        return clone(r.result) as T;
      }
      const result = await handle(method, path, body as RecordData, headers);
      if (method !== "GET") save();
      if (receiptKey)
        receipts.set(receiptKey, { body: serialized, result: clone(result) });
      return clone(result) as T;
    },
  };
}
