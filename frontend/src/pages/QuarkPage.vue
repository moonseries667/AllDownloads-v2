<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { useRoute } from "vue-router";
const route = useRoute();
import {
  ArrowUp,
  Check,
  ChevronRight,
  CircleAlert,
  Clock3,
  Cloud,
  Download,
  File,
  Folder,
  FolderOpen,
  HardDrive,
  Plus,
  RefreshCw,
  Search,
} from "lucide-vue-next";
import { api, dataRevision, notifyError, notifySuccess } from "../api/client";
import DownloadPreview from "../components/DownloadPreview.vue";
import StatusBadge from "../components/StatusBadge.vue";
import { formatBytes, formatDate } from "../format";

type Share = {
  id: string;
  accountId?: string;
  externalShareId?: string;
  title?: string | null;
  targetFolderId?: string | null;
  targetPath?: string | null;
  shareFolderKey?: string | null;
  watchEnabled: boolean;
  updateState?: string | null;
  lastCheckedAt?: string | null;
  nextCheckAt?: string | null;
  revision?: string;
  error?: string | { message?: string } | null;
};
type CloudNode = {
  id: string;
  shareId: string;
  parentId: string | null;
  externalId?: string | null;
  version?: string | null;
  name: string;
  kind: "FILE" | "FOLDER" | string;
  relativePath?: string | null;
  sizeBytes?: string | number | null;
  modifiedAt?: string | null;
  availability?: string | null;
  downloadSummary?: { state?: string | null; [key: string]: unknown } | null;
};
type QuarkFolder = {
  id: string;
  parentId?: string | null;
  name: string;
  path?: string | null;
  relativePath?: string | null;
};
type FolderTreeNode = {
  id: string;
  label: string;
  kind: "ROOT" | "FOLDER";
  parentId: string | null;
  children: FolderTreeNode[];
};
type DraftEntry = { key: string; url: string };
type ShareResult = {
  reused?: boolean;
  url?: string;
  shareId?: string;
  taskId?: string;
  error?: string | { message?: string };
};

const ACCOUNT_ID = "q-main";
const shares = ref<Share[]>([]);
const folders = ref<QuarkFolder[]>([]);
const settings = ref<Record<string, any> | null>(null);
const nodesByShare = ref<Record<string, CloudNode[]>>({});
const selectedShareId = ref<string | null>("q1");
const currentParentId = ref<string | null>(null);
const selectedNodeIds = ref<Set<string>>(new Set());
const searchText = ref("");
const searchScope = ref<"CURRENT" | "SHARE">("CURRENT");
const pageLoading = ref(false);
const nodesLoading = ref(false);
const loadError = ref("");
const nodeError = ref("");
const watchPending = ref<Set<string>>(new Set());
const actionPending = ref<Record<string, string>>({});
const tableRef = ref<any>();
const loadSequence = ref(0);
const nodeSequence = ref(0);

const addDialogVisible = ref(false);
const shareLinksText = ref("");
const useOneTargetFolder = ref(true);
const bulkTargetFolderId = ref("");
const rowTargetFolderIds = ref<Record<string, string>>({});
const submittingShares = ref(false);
const shareFormError = ref("");
const shareCreateResult = ref<{
  batchId?: string;
  entries: ShareResult[];
} | null>(null);

const previewVisible = ref(false);
const previewSelection = ref<{
  mode: "EXPLICIT";
  resource: "QUARK_NODES";
  ids: string[];
}>({
  mode: "EXPLICIT",
  resource: "QUARK_NODES",
  ids: [],
});

const activeShare = computed(
  () =>
    shares.value.find((share) => share.id === selectedShareId.value) ?? null,
);
const activeNodes = computed(() =>
  selectedShareId.value
    ? (nodesByShare.value[selectedShareId.value] ?? [])
    : [],
);
const activeNodeMap = computed(
  () =>
    new Map<string, CloudNode>(
      activeNodes.value.map((node) => [node.id, node] as const),
    ),
);
const allLoadedNodeMap = computed(() => {
  const map = new Map<string, CloudNode>();
  Object.values(nodesByShare.value).forEach((items) =>
    items.forEach((node) => map.set(node.id, node)),
  );
  return map;
});
const downloadRoot = computed(() => {
  const value = settings.value?.downloadRoot;
  if (typeof value === "string") return value;
  return "";
});
const folderLabelMap = computed(
  () =>
    new Map<string, string>(
      folders.value.map((folder) => [folder.id, folderLabel(folder)] as const),
    ),
);
const folderOptions = computed(() =>
  folders.value.map((folder) => ({
    ...folder,
    displayLabel: folderLabel(folder),
  })),
);
const currentFolderMissing = computed(() =>
  Boolean(
    currentParentId.value && !activeNodeMap.value.has(currentParentId.value),
  ),
);
const activeShareIssue = computed(() => {
  const error = activeShare.value?.error;
  if (!error) return "";
  return typeof error === "string"
    ? error
    : error.message || "该分享存在需要处理的问题";
});
const activeShareTarget = computed(() => {
  if (!activeShare.value) return "尚未选择分享目录";
  return (
    activeShare.value.targetPath ||
    folderLabelMap.value.get(activeShare.value.targetFolderId || "") ||
    "目标目录未知"
  );
});
const targetRows = computed<DraftEntry[]>(() => {
  const counts = new Map<string, number>();
  return shareLinksText.value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .map((url) => {
      const occurrence = counts.get(url) ?? 0;
      counts.set(url, occurrence + 1);
      return { key: url + "::" + occurrence, url };
    });
});
const shareCreateEntries = computed(
  () => shareCreateResult.value?.entries ?? [],
);
const selectedCount = computed(() => selectedNodeIds.value.size);
const loadedSelectedNodes = computed(() =>
  [...selectedNodeIds.value]
    .map((id) => allLoadedNodeMap.value.get(id))
    .filter((node): node is CloudNode => Boolean(node)),
);
const watchEnabledCount = computed(
  () => shares.value.filter((share) => share.watchEnabled).length,
);

const folderTree = computed<FolderTreeNode[]>(() => {
  const share = activeShare.value;
  if (!share) return [];
  const childFolders = new Map<string | null, CloudNode[]>();
  activeNodes.value.filter(isFolder).forEach((node) => {
    const parent = node.parentId ?? null;
    const group = childFolders.get(parent) ?? [];
    group.push(node);
    childFolders.set(parent, group);
  });
  const buildChildren = (parentId: string | null): FolderTreeNode[] =>
    (childFolders.get(parentId) ?? [])
      .sort((a, b) => a.name.localeCompare(b.name, "zh-CN"))
      .map((node) => ({
        id: node.id,
        label: node.name,
        kind: "FOLDER" as const,
        parentId: node.parentId ?? null,
        children: buildChildren(node.id),
      }));
  return [
    {
      id: rootTreeKey(share.id),
      label: share.title || "分享根目录",
      kind: "ROOT",
      parentId: null,
      children: buildChildren(null),
    },
  ];
});
const treeCurrentKey = computed(
  () =>
    currentParentId.value ??
    (activeShare.value ? rootTreeKey(activeShare.value.id) : ""),
);
const visibleNodes = computed(() => {
  const query = searchText.value.trim().toLocaleLowerCase();
  const inShareSearch = searchScope.value === "SHARE" && query.length > 0;
  let rows = inShareSearch
    ? activeNodes.value.filter((node) => matchesNode(node, query))
    : activeNodes.value.filter(
        (node) => (node.parentId ?? null) === currentParentId.value,
      );
  if (!inShareSearch && query)
    rows = rows.filter((node) => matchesNode(node, query));
  return rows.sort((a, b) => {
    const folderOrder = Number(!isFolder(a)) - Number(!isFolder(b));
    return folderOrder || a.name.localeCompare(b.name, "zh-CN");
  });
});
const breadcrumbs = computed(() => {
  const result: Array<{ id: string | null; label: string }> = [
    { id: null, label: activeShare.value?.title || "分享根目录" },
  ];
  if (!currentParentId.value) return result;
  const chain: CloudNode[] = [];
  let node = activeNodeMap.value.get(currentParentId.value);
  const seen = new Set<string>();
  while (node && !seen.has(node.id)) {
    seen.add(node.id);
    chain.unshift(node);
    node = node.parentId ? activeNodeMap.value.get(node.parentId) : undefined;
  }
  chain.forEach((item) => result.push({ id: item.id, label: item.name }));
  if (currentFolderMissing.value)
    result.push({ id: currentParentId.value, label: "目录已变化" });
  return result;
});

function isFolder(node: CloudNode) {
  return String(node.kind).toUpperCase() === "FOLDER";
}
function rootTreeKey(shareId: string) {
  return "share-root:" + shareId;
}
function folderLabel(folder: QuarkFolder) {
  return folder.path || folder.relativePath || folder.name || folder.id;
}
function matchesNode(node: CloudNode, query: string) {
  return (node.name + " " + (node.relativePath ?? ""))
    .toLocaleLowerCase()
    .includes(query);
}
function nodePath(node: CloudNode) {
  if (node.relativePath) return node.relativePath;
  const names: string[] = [];
  let current: CloudNode | undefined = node;
  const seen = new Set<string>();
  while (current && !seen.has(current.id)) {
    seen.add(current.id);
    names.unshift(current.name);
    current = current.parentId
      ? activeNodeMap.value.get(current.parentId)
      : undefined;
  }
  return names.join("/");
}
function fileType(node: CloudNode) {
  if (isFolder(node)) return "文件夹";
  const ext = node.name.includes(".")
    ? node.name.split(".").pop()?.toUpperCase()
    : "";
  return ext ? ext + " 文件" : "文件";
}
function downloadState(node: CloudNode) {
  return node.downloadSummary?.state || "NOT_REQUESTED";
}
function shareStateLabel(share: Share) {
  const labels: Record<string, string> = {
    RUNNING: "正在检查",
    QUEUED: "等待检查",
    SUCCEEDED: "更新完成",
    UPDATED: "已转存新增内容",
    PARTIAL: "部分完成",
    FAILED: "检查失败",
    NEEDS_ACTION: "需要处理",
    WAITING_RETRY: "等待重试",
    PAUSED: "已暂停",
    ACTIVE: "追更中",
  };
  return labels[share.updateState || ""] || undefined;
}
function rowClassName({ row }: { row: CloudNode }) {
  return selectedNodeIds.value.has(row.id) ? "is-selected-row" : "";
}

function setSelection(rows: CloudNode[]) {
  const visibleIds = new Set(visibleNodes.value.map((node) => node.id));
  const selected = new Set(selectedNodeIds.value);
  visibleIds.forEach((id) => selected.delete(id));
  rows.forEach((node) => selected.add(node.id));
  selectedNodeIds.value = selected;
}
async function syncTableSelection() {
  await nextTick();
  const table = tableRef.value;
  if (!table) return;
  visibleNodes.value.forEach((node) =>
    table.toggleRowSelection(node, selectedNodeIds.value.has(node.id), false),
  );
}
function navigateTo(parentId: string | null) {
  currentParentId.value = parentId;
  searchScope.value = "CURRENT";
  searchText.value = "";
}
function navigateUp() {
  if (!currentParentId.value) return;
  const current = activeNodeMap.value.get(currentParentId.value);
  navigateTo(current?.parentId ?? null);
}
function onTreeNodeClick(data: FolderTreeNode) {
  navigateTo(data.kind === "ROOT" ? null : data.id);
}
async function selectShare(shareId: string) {
  if (selectedShareId.value === shareId) return;
  selectedShareId.value = shareId;
  currentParentId.value = null;
  searchText.value = "";
  searchScope.value = "CURRENT";
  await loadNodes(shareId);
}
async function loadNodes(shareId: string, quiet = false) {
  const sequence = ++nodeSequence.value;
  nodesLoading.value = true;
  nodeError.value = "";
  try {
    const query = new URLSearchParams({ shareId });
    const result = { items: [] as CloudNode[] };
    let cursor: string | null = null;
    do {
      if (cursor) query.set("cursor", cursor);
      const part = await api.get<{
        items: CloudNode[];
        nextCursor: string | null;
      }>("/quark/nodes?" + query.toString());
      if (sequence !== nodeSequence.value) return;
      result.items.push(...part.items);
      cursor = part.nextCursor;
    } while (cursor);
    if (sequence !== nodeSequence.value) return;
    nodesByShare.value = {
      ...nodesByShare.value,
      [shareId]: result.items ?? [],
    };
    if (selectedShareId.value === shareId) await syncTableSelection();
  } catch (error) {
    if (sequence !== nodeSequence.value) return;
    nodeError.value =
      error instanceof Error ? error.message : "目录加载失败，请重试";
    if (!quiet) notifyError(error);
  } finally {
    if (sequence === nodeSequence.value) nodesLoading.value = false;
  }
}
async function load() {
  const sequence = ++loadSequence.value;
  pageLoading.value = true;
  loadError.value = "";
  const results = await Promise.allSettled([
    api.get<{ items: Share[] }>("/quark/shares"),
    api.get<{ items: QuarkFolder[] }>(
      "/quark/folders?accountId=" + encodeURIComponent(ACCOUNT_ID),
    ),
    api.get<Record<string, any>>("/settings"),
  ]);
  if (sequence !== loadSequence.value) return;
  const failures: unknown[] = [];
  const shareResult = results[0];
  const folderResult = results[1];
  const settingsResult = results[2];
  if (shareResult.status === "fulfilled") {
    shares.value = shareResult.value.items ?? [];
    if (!shares.value.some((share) => share.id === selectedShareId.value)) {
      selectedShareId.value = shares.value[0]?.id ?? null;
      currentParentId.value = null;
    }
    if (!selectedShareId.value && shares.value[0])
      selectedShareId.value = shares.value[0].id;
  } else failures.push(shareResult.reason);
  if (folderResult.status === "fulfilled")
    folders.value = folderResult.value.items ?? [];
  else failures.push(folderResult.reason);
  if (settingsResult.status === "fulfilled")
    settings.value = settingsResult.value;
  else failures.push(settingsResult.reason);
  if (
    selectedShareId.value &&
    (shareResult.status === "fulfilled" ||
      !nodesByShare.value[selectedShareId.value])
  ) {
    await loadNodes(selectedShareId.value, true);
  }
  if (sequence !== loadSequence.value) return;
  pageLoading.value = false;
  if (failures.length) {
    loadError.value =
      failures[0] instanceof Error
        ? failures[0].message
        : "部分页面信息暂时无法读取";
    notifyError(failures[0]);
  }
  await syncTableSelection();
}

async function toggleWatch(share: Share, value: unknown) {
  if (!share.revision) {
    notifyError(new Error("分享版本信息缺失，暂时不能修改追更设置"));
    return;
  }
  const enabled = Boolean(value);
  const pending = new Set(watchPending.value);
  pending.add(share.id);
  watchPending.value = pending;
  try {
    await api.patch(
      "/quark/shares/" + encodeURIComponent(share.id),
      { watchEnabled: enabled },
      share.revision,
    );
    notifySuccess(enabled ? "追更已启用" : "追更已暂停");
  } catch (error) {
    notifyError(error);
  } finally {
    const next = new Set(watchPending.value);
    next.delete(share.id);
    watchPending.value = next;
  }
}
async function runShareAction(
  share: Share,
  action: "refresh" | "refresh-tree",
) {
  actionPending.value = { ...actionPending.value, [share.id]: action };
  try {
    await api.post(
      "/quark/shares/" + encodeURIComponent(share.id) + "/" + action,
    );
    notifySuccess(
      action === "refresh"
        ? "检查请求已受理，结果会更新在分享追更状态中。"
        : "目录刷新请求已受理，网盘文件事实更新后会显示。",
    );
  } catch (error) {
    notifyError(error);
  } finally {
    const next = { ...actionPending.value };
    delete next[share.id];
    actionPending.value = next;
  }
}
function openAddDialog() {
  shareLinksText.value = "";
  bulkTargetFolderId.value = "";
  rowTargetFolderIds.value = {};
  useOneTargetFolder.value = true;
  shareFormError.value = "";
  shareCreateResult.value = null;
  addDialogVisible.value = true;
}
function targetFolderFor(entry: DraftEntry) {
  return useOneTargetFolder.value
    ? bulkTargetFolderId.value
    : rowTargetFolderIds.value[entry.key] || "";
}
async function submitShares() {
  shareFormError.value = "";
  shareCreateResult.value = null;
  if (!targetRows.value.length) {
    shareFormError.value = "请先粘贴至少一条分享链接，每行一条。";
    return;
  }
  if (targetRows.value.some((entry) => !targetFolderFor(entry))) {
    shareFormError.value = useOneTargetFolder.value
      ? "请选择本批分享的目标夸克目录。"
      : "请为每条分享指定目标夸克目录。";
    return;
  }
  submittingShares.value = true;
  try {
    const result = await api.post<{ batchId?: string; items: ShareResult[] }>(
      "/quark/shares",
      {
        accountId: ACCOUNT_ID,
        entries: targetRows.value.map((entry) => ({
          url: entry.url,
          targetFolderId: targetFolderFor(entry),
        })),
      },
    );
    shareCreateResult.value = {
      batchId: result.batchId,
      entries: result.items,
    };
    const acceptedCount = shareCreateResult.value.entries.filter(
      (entry) =>
        !entry.error && !entry.reused && (entry.shareId || entry.taskId),
    ).length;
    notifySuccess(
      "转存与追更请求已受理" +
        (acceptedCount ? " " + acceptedCount + " 条" : "") +
        "；下载仍需单独选择文件。",
    );
  } catch (error) {
    shareFormError.value =
      error instanceof Error
        ? error.message
        : "分享请求未能提交，请保留链接后重试。";
    notifyError(error);
  } finally {
    submittingShares.value = false;
  }
}
function entryResultText(entry: ShareResult) {
  if (entry.error)
    return typeof entry.error === "string"
      ? entry.error
      : entry.error.message || "该条目未受理";
  if (entry.reused) return "已复用已有分享 · " + entry.shareId;
  if (entry.taskId) return "已受理 · 任务 " + entry.taskId;
  if (entry.shareId) return "已受理 · 分享 " + entry.shareId;
  return "已提交，等待服务端返回逐条结果";
}

function openDownloadPreview() {
  if (!selectedNodeIds.value.size) return;
  previewSelection.value = {
    mode: "EXPLICIT",
    resource: "QUARK_NODES",
    ids: [...selectedNodeIds.value],
  };
  previewVisible.value = true;
}
function onPreviewSubmitted() {
  const next = new Set(selectedNodeIds.value);
  previewSelection.value.ids.forEach((id) => next.delete(id));
  selectedNodeIds.value = next;
  void syncTableSelection();
}
function clearSelection() {
  selectedNodeIds.value = new Set();
  void syncTableSelection();
}
watch(
  dataRevision,
  () => {
    void load();
  },
  { immediate: true },
);
watch(
  () => route.query.q,
  (value) => {
    if (route.path === "/quark/cloud" && value) {
      searchText.value = String(value);
      searchScope.value = "SHARE";
    }
  },
  { immediate: true },
);
watch(
  [visibleNodes, selectedNodeIds],
  () => {
    void syncTableSelection();
  },
  { deep: true },
);
</script>

<template>
  <main class="quark-page">
    <header class="page-header quark-header">
      <div class="quark-heading">
        <span class="quark-heading-icon" aria-hidden="true"
          ><Cloud :size="20"
        /></span>
        <div>
          <p class="quark-eyebrow">QUARK CLOUD</p>
          <h1 class="page-title">夸克云盘</h1>
          <p class="page-subtitle">
            管理已转存分享和网盘文件；下载由你单独选择并确认。
          </p>
        </div>
      </div>
      <div class="header-actions">
        <el-button :loading="pageLoading" @click="load"
          ><RefreshCw :size="16" />刷新页面</el-button
        >
        <el-button type="primary" @click="openAddDialog"
          ><Plus :size="16" />添加分享转存</el-button
        >
      </div>
    </header>

    <el-alert
      v-if="loadError"
      class="load-alert"
      type="warning"
      :closable="false"
      show-icon
    >
      <template #title>部分信息暂时不可用，已保留上次读取的内容。</template>
      <span>{{ loadError }}</span>
    </el-alert>

    <section class="quark-overview" aria-label="夸克页面概况">
      <div class="overview-item">
        <span class="overview-icon"
          ><Cloud :size="17" aria-hidden="true"
        /></span>
        <div>
          <span class="overview-label">管理中的分享</span
          ><strong>{{ shares.length }}</strong>
        </div>
      </div>
      <div class="overview-item">
        <span class="overview-icon is-green"
          ><RefreshCw :size="17" aria-hidden="true"
        /></span>
        <div>
          <span class="overview-label">正在追更</span
          ><strong>{{ watchEnabledCount }}</strong>
        </div>
      </div>
      <div
        class="download-root"
        :title="downloadRoot || '统一下载根目录尚未配置'"
      >
        <span class="overview-icon is-neutral"
          ><HardDrive :size="17" aria-hidden="true"
        /></span>
        <div class="download-root-copy">
          <span class="overview-label">本机下载目录（只读）</span>
          <strong>{{ downloadRoot || "尚未配置统一下载目录" }}</strong>
        </div>
      </div>
    </section>

    <section class="cloud-workspace" aria-label="夸克分享与文件浏览">
      <aside class="cloud-sidebar panel">
        <div class="sidebar-section">
          <div class="section-heading">
            <h2 class="section-title">我的分享</h2>
            <span class="section-count">{{ shares.length }}</span>
          </div>
          <div v-if="!shares.length && !pageLoading" class="share-empty">
            <Cloud :size="19" aria-hidden="true" /><span
              >添加一个分享后，它会显示在这里。</span
            >
          </div>
          <div v-else class="share-list">
            <article
              v-for="share in shares"
              :key="share.id"
              class="share-card"
              :class="{ active: selectedShareId === share.id }"
            >
              <button
                class="share-pick"
                type="button"
                :aria-current="
                  selectedShareId === share.id ? 'page' : undefined
                "
                @click="selectShare(share.id)"
              >
                <span class="share-mark" aria-hidden="true"
                  ><Cloud :size="15"
                /></span>
                <span class="share-main">
                  <strong class="share-title">{{
                    share.title || "未命名分享"
                  }}</strong>
                  <span
                    class="share-target"
                    :title="
                      share.targetPath ||
                      folderLabelMap.get(share.targetFolderId || '') ||
                      ''
                    "
                    >{{
                      share.targetPath ||
                      folderLabelMap.get(share.targetFolderId || "") ||
                      "目标目录未知"
                    }}</span
                  >
                </span>
                <ChevronRight
                  class="share-chevron"
                  :size="15"
                  aria-hidden="true"
                />
              </button>
              <div class="share-card-meta">
                <StatusBadge
                  :state="share.updateState || 'UNKNOWN'"
                  :label="shareStateLabel(share)"
                />
                <span class="share-checked"
                  ><Clock3 :size="12" aria-hidden="true" />{{
                    formatDate(share.lastCheckedAt)
                  }}</span
                >
              </div>
              <div class="share-card-actions">
                <label class="watch-control">
                  <span>追更</span>
                  <el-switch
                    :model-value="share.watchEnabled"
                    :loading="watchPending.has(share.id)"
                    :aria-label="
                      (share.watchEnabled ? '停用' : '启用') +
                      (share.title || '此分享') +
                      '的追更'
                    "
                    @change="toggleWatch(share, $event)"
                  />
                </label>
                <el-tooltip
                  content="立即检查分享更新，不会开始下载"
                  placement="top"
                >
                  <el-button
                    text
                    circle
                    :loading="actionPending[share.id] === 'refresh'"
                    :aria-label="'立即检查' + (share.title || '此分享')"
                    @click="runShareAction(share, 'refresh')"
                    ><RefreshCw :size="15"
                  /></el-button>
                </el-tooltip>
                <el-tooltip content="刷新该分享的网盘目录" placement="top">
                  <el-button
                    text
                    circle
                    :loading="actionPending[share.id] === 'refresh-tree'"
                    :aria-label="'刷新' + (share.title || '此分享') + '的目录'"
                    @click="runShareAction(share, 'refresh-tree')"
                    ><FolderOpen :size="15"
                  /></el-button>
                </el-tooltip>
              </div>
              <p v-if="share.error" class="share-error">
                <CircleAlert :size="13" aria-hidden="true" />{{
                  typeof share.error === "string"
                    ? share.error
                    : share.error.message
                }}
              </p>
            </article>
          </div>
        </div>

        <div class="sidebar-divider"></div>

        <div class="sidebar-section tree-section">
          <div class="section-heading">
            <h2 class="section-title">分享目录</h2>
            <span
              v-if="activeShare"
              class="tree-share-name"
              :title="activeShare.title || ''"
              >{{ activeShare.title || "未命名分享" }}</span
            >
          </div>
          <el-tree
            v-if="activeShare"
            :key="activeShare.id"
            :data="folderTree"
            node-key="id"
            :props="{ label: 'label', children: 'children' }"
            :current-node-key="treeCurrentKey"
            :default-expanded-keys="[rootTreeKey(activeShare.id)]"
            :expand-on-click-node="false"
            highlight-current
            class="share-tree"
            @node-click="onTreeNodeClick"
          >
            <template #default="{ data }">
              <span class="tree-node-label">
                <component
                  :is="data.kind === 'ROOT' ? Cloud : Folder"
                  :size="15"
                  aria-hidden="true"
                />
                <span>{{ data.label }}</span>
              </span>
            </template>
          </el-tree>
          <div v-else class="tree-empty">选择或添加分享后，可浏览其目录。</div>
          <div v-if="nodeError" class="node-error">
            <CircleAlert :size="14" aria-hidden="true" />{{ nodeError }}
          </div>
        </div>
      </aside>

      <section class="cloud-browser panel">
        <div class="browser-topline">
          <div class="browser-heading">
            <span class="browser-kicker">网盘文件</span>
            <div class="browser-title-row">
              <h2 class="section-title">
                {{ activeShare?.title || "选择一个分享" }}
              </h2>
              <StatusBadge
                v-if="activeShare"
                :state="activeShare.updateState || 'UNKNOWN'"
                :label="shareStateLabel(activeShare)"
              />
            </div>
            <p class="browser-target">
              <span>夸克目标目录</span><strong>{{ activeShareTarget }}</strong>
            </p>
          </div>
          <div class="browser-actions">
            <el-button
              v-if="activeShare"
              :loading="actionPending[activeShare.id] === 'refresh-tree'"
              @click="runShareAction(activeShare, 'refresh-tree')"
              ><FolderOpen :size="15" />刷新目录</el-button
            >
            <el-button
              v-if="activeShare"
              :loading="actionPending[activeShare.id] === 'refresh'"
              @click="runShareAction(activeShare, 'refresh')"
              ><RefreshCw :size="15" />立即检查</el-button
            >
          </div>
        </div>

        <div v-if="activeShareIssue" class="share-issue">
          <CircleAlert :size="15" aria-hidden="true" /><span>{{
            activeShareIssue
          }}</span>
        </div>

        <div class="browser-toolbar">
          <div class="breadcrumbs" aria-label="目录位置">
            <el-button
              text
              :disabled="!currentParentId || nodesLoading"
              aria-label="回到上级目录"
              @click="navigateUp"
              ><ArrowUp :size="15" />上级</el-button
            >
            <el-breadcrumb separator="/">
              <el-breadcrumb-item
                v-for="(crumb, index) in breadcrumbs"
                :key="(crumb.id || 'root') + '-' + index"
              >
                <button
                  class="breadcrumb-link"
                  type="button"
                  :aria-current="
                    index === breadcrumbs.length - 1 ? 'location' : undefined
                  "
                  @click="navigateTo(crumb.id)"
                >
                  {{ crumb.label }}
                </button>
              </el-breadcrumb-item>
            </el-breadcrumb>
          </div>
          <div class="search-box">
            <el-input
              v-model="searchText"
              clearable
              placeholder="搜索文件或文件夹"
              aria-label="搜索当前目录或整个分享"
            >
              <template #prefix
                ><Search :size="15" aria-hidden="true"
              /></template>
            </el-input>
            <el-select
              v-model="searchScope"
              aria-label="搜索范围"
              class="search-scope"
              :disabled="!searchText.trim()"
            >
              <el-option label="当前目录" value="CURRENT" /><el-option
                label="整个分享"
                value="SHARE"
              />
            </el-select>
          </div>
        </div>

        <div
          v-if="currentFolderMissing && searchScope === 'CURRENT'"
          class="folder-changed"
        >
          <CircleAlert
            :size="15"
            aria-hidden="true"
          />当前目录已不在最新目录清单中。请返回分享根目录核对后再继续。
          <el-button link type="primary" @click="navigateTo(null)"
            >回到分享根目录</el-button
          >
        </div>

        <div v-if="selectedCount" class="selection-bar">
          <div class="selection-count">
            <span class="selection-check">{{ selectedCount }}</span
            ><span>个对象已选</span
            ><button
              type="button"
              class="clear-selection"
              @click="clearSelection"
            >
              清除
            </button>
          </div>
          <el-button
            type="primary"
            :disabled="!loadedSelectedNodes.length"
            @click="openDownloadPreview"
            ><Download :size="16" />预览下载</el-button
          >
        </div>

        <div class="table-viewport" :aria-busy="nodesLoading">
          <el-table
            ref="tableRef"
            v-loading="nodesLoading || pageLoading"
            :data="visibleNodes"
            row-key="id"
            class="quark-table"
            :row-class-name="rowClassName"
            @selection-change="setSelection"
          >
            <el-table-column type="selection" width="48" reserve-selection />
            <el-table-column label="名称" min-width="230">
              <template #default="{ row }">
                <button
                  v-if="isFolder(row)"
                  class="node-name folder-name"
                  type="button"
                  @click="navigateTo(row.id)"
                >
                  <Folder :size="17" aria-hidden="true" /><span>{{
                    row.name
                  }}</span
                  ><ChevronRight :size="14" aria-hidden="true" />
                </button>
                <span v-else class="node-name file-name"
                  ><File :size="17" aria-hidden="true" /><span>{{
                    row.name
                  }}</span></span
                >
              </template>
            </el-table-column>
            <el-table-column label="类型" width="112">
              <template #default="{ row }"
                ><span class="type-cell"
                  ><Folder
                    v-if="isFolder(row)"
                    :size="14"
                    aria-hidden="true"
                  /><File v-else :size="14" aria-hidden="true" />{{
                    fileType(row)
                  }}</span
                ></template
              >
            </el-table-column>
            <el-table-column label="大小" width="118" align="right">
              <template #default="{ row }">{{
                isFolder(row) ? "—" : formatBytes(row.sizeBytes)
              }}</template>
            </el-table-column>

            <el-table-column label="更新时间" width="190">
              <template #default="{ row }">{{
                formatDate(row.modifiedAt)
              }}</template>
            </el-table-column>
            <el-table-column label="本机下载状态" width="150">
              <template #default="{ row }"
                ><StatusBadge :state="downloadState(row)"
              /></template>
            </el-table-column>
            <el-table-column
              v-if="searchScope === 'SHARE' && searchText.trim()"
              label="所在目录"
              min-width="190"
              show-overflow-tooltip
            >
              <template #default="{ row }">{{ nodePath(row) }}</template>
            </el-table-column>
            <template #empty>
              <el-empty
                v-if="!activeShare"
                description="先从左侧选择一个已转存分享"
                :image-size="54"
              />
              <el-empty
                v-else-if="searchText.trim()"
                description="没有匹配的网盘文件"
                :image-size="54"
              />
              <el-empty
                v-else
                description="此目录当前没有已收录的文件"
                :image-size="54"
              />
            </template>
          </el-table>
        </div>
        <div class="table-footnote">
          <span>{{ visibleNodes.length }} 个目录项</span>
          <span v-if="searchText.trim() && searchScope === 'SHARE'"
            >搜索范围：{{ activeShare?.title || "当前分享" }}</span
          >
          <span v-else>选择文件夹时，后端会展开该文件夹的完整子树。</span>
        </div>
      </section>
    </section>

    <el-dialog
      v-model="addDialogVisible"
      title="添加分享转存"
      width="min(840px, calc(100vw - 48px))"
      class="share-dialog"
      destroy-on-close
    >
      <div class="dialog-intro">
        <span class="dialog-icon"><Cloud :size="18" aria-hidden="true" /></span>
        <p>提交后会转存分享并建立更新维护。此操作不会创建下载任务。</p>
      </div>
      <el-form label-position="top" @submit.prevent="submitShares">
        <el-form-item label="分享链接" required>
          <el-input
            v-model="shareLinksText"
            type="textarea"
            :rows="5"
            resize="vertical"
            placeholder="每行粘贴一条夸克分享链接"
            aria-label="夸克分享链接，每行一条"
            @input="shareCreateResult = null"
          />
          <div class="field-hint">
            支持多条链接；每条分享可以使用相同目录，也可以单独指定目录。
          </div>
        </el-form-item>
        <div class="folder-mode-row">
          <div>
            <strong>目标夸克目录</strong
            ><span>决定转存副本在夸克网盘中的位置</span>
          </div>
          <el-radio-group
            v-model="useOneTargetFolder"
            aria-label="目标目录指定方式"
          >
            <el-radio-button :value="true">整批使用同一目录</el-radio-button>
            <el-radio-button :value="false">逐条指定</el-radio-button>
          </el-radio-group>
        </div>
        <el-form-item v-if="useOneTargetFolder" label="本批目标目录" required>
          <el-select
            v-model="bulkTargetFolderId"
            filterable
            clearable
            placeholder="选择账号中的夸克文件夹"
            class="folder-select"
            aria-label="本批转存目标夸克文件夹"
          >
            <el-option
              v-for="folder in folderOptions"
              :key="folder.id"
              :label="folder.displayLabel"
              :value="folder.id"
            />
          </el-select>
          <div v-if="!folders.length" class="field-hint">
            当前没有可选目录，或目录列表暂时不可用。
          </div>
        </el-form-item>
        <div v-else class="entry-target-list">
          <div
            v-for="(entry, index) in targetRows"
            :key="entry.key"
            class="entry-target-row"
          >
            <div class="entry-target-link">
              <span class="entry-index">{{ index + 1 }}</span
              ><span :title="entry.url">{{ entry.url }}</span>
            </div>
            <el-select
              v-model="rowTargetFolderIds[entry.key]"
              filterable
              clearable
              placeholder="选择目标夸克文件夹"
              class="folder-select"
              :aria-label="'第 ' + (index + 1) + ' 条分享的目标文件夹'"
            >
              <el-option
                v-for="folder in folderOptions"
                :key="folder.id"
                :label="folder.displayLabel"
                :value="folder.id"
              />
            </el-select>
          </div>
          <div v-if="!targetRows.length" class="entry-target-empty">
            粘贴分享链接后，可为每条链接选择目录。
          </div>
        </div>

        <div v-if="shareFormError" class="form-error" role="alert">
          <CircleAlert :size="15" aria-hidden="true" />{{ shareFormError }}
        </div>
        <div v-if="shareCreateResult" class="create-result" role="status">
          <div class="create-result-heading">
            <span><Check :size="16" aria-hidden="true" />请求已提交</span
            ><small v-if="shareCreateResult.batchId"
              >批次 {{ shareCreateResult.batchId }}</small
            >
          </div>
          <div v-if="shareCreateEntries.length" class="create-result-list">
            <div
              v-for="(entry, index) in shareCreateEntries"
              :key="(entry.url || index) + '-' + index"
              class="create-result-row"
              :class="{ failed: entry.error }"
            >
              <span class="entry-index">{{ index + 1 }}</span
              ><span class="result-url">{{
                entry.url || targetRows[index]?.url || "分享条目"
              }}</span
              ><span>{{ entryResultText(entry) }}</span>
            </div>
          </div>
          <p v-else>
            请求已受理。页面刷新后可查看分享与追更状态；转存完成不代表已下载。
          </p>
        </div>
      </el-form>
      <template #footer>
        <el-button @click="addDialogVisible = false">关闭</el-button>
        <el-button
          type="primary"
          :loading="submittingShares"
          :disabled="Boolean(shareCreateResult)"
          @click="submitShares"
          ><Cloud :size="16" />转存并维护更新</el-button
        >
      </template>
    </el-dialog>

    <DownloadPreview
      v-model="previewVisible"
      platform="QUARK"
      :selection="previewSelection"
      @submitted="onPreviewSubmitted"
    />
  </main>
</template>

<style scoped>
.quark-page {
  --quark-primary: #166a59;
  --quark-primary-strong: #115647;
  --quark-primary-soft: #eef7f4;
  --quark-ink: #202a28;
  --quark-muted: #687571;
  --quark-line: #e5ebe8;
  --quark-surface: #fff;
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 18px;
  color: var(--quark-ink);
  font-size: 14px;
}
.quark-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
}
.quark-heading {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  gap: 13px;
}
.quark-heading-icon {
  display: grid;
  width: 42px;
  height: 42px;
  flex: 0 0 42px;
  place-items: center;
  color: var(--quark-primary);
  background: var(--quark-primary-soft);
  border: 1px solid #dcece6;
  border-radius: 12px;
}
.quark-eyebrow {
  margin: 0 0 3px;
  color: #73807c;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.1em;
}
.quark-header :deep(.page-title) {
  margin: 0;
  color: var(--quark-ink);
  font-size: 23px;
  line-height: 1.35;
}
.quark-header :deep(.page-subtitle) {
  margin-top: 4px;
  color: var(--quark-muted);
  font-size: 14px;
  line-height: 1.5;
}
.header-actions,
.browser-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
.header-actions :deep(.el-button),
.browser-actions :deep(.el-button) {
  min-height: 38px;
  font-size: 14px;
}
.header-actions :deep(.el-button--primary),
.selection-bar :deep(.el-button--primary),
.share-dialog :deep(.el-button--primary) {
  --el-button-bg-color: var(--quark-primary);
  --el-button-border-color: var(--quark-primary);
  --el-button-hover-bg-color: var(--quark-primary-strong);
  --el-button-hover-border-color: var(--quark-primary-strong);
  --el-button-active-bg-color: var(--quark-primary-strong);
  --el-button-active-border-color: var(--quark-primary-strong);
}
.load-alert {
  margin-top: -3px;
}

.quark-overview {
  display: grid;
  grid-template-columns: minmax(175px, 0.75fr) minmax(175px, 0.75fr) minmax(
      260px,
      2fr
    );
  gap: 10px;
}
.overview-item,
.download-root {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 11px;
  padding: 13px 15px;
  background: var(--quark-surface);
  border: 1px solid var(--quark-line);
  border-radius: 10px;
}
.overview-icon {
  display: grid;
  width: 34px;
  height: 34px;
  flex: 0 0 34px;
  place-items: center;
  color: #63716d;
  background: #f4f6f5;
  border-radius: 9px;
}
.overview-icon.is-green {
  color: var(--quark-primary);
  background: var(--quark-primary-soft);
}
.overview-icon.is-neutral {
  color: #57645f;
  background: #f1f4f2;
}
.overview-item div,
.download-root-copy {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 3px;
}
.overview-label {
  color: var(--quark-muted);
  font-size: 12px;
}
.overview-item strong {
  color: var(--quark-ink);
  font-size: 17px;
  line-height: 1.2;
}
.download-root-copy strong {
  overflow: hidden;
  color: var(--quark-ink);
  font-size: 13px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.cloud-workspace {
  display: grid;
  min-width: 0;
  grid-template-columns: minmax(245px, 285px) minmax(0, 1fr);
  align-items: start;
  gap: 14px;
}
.panel {
  min-width: 0;
  background: var(--quark-surface);
  border: 1px solid var(--quark-line);
  border-radius: 12px;
}
.cloud-sidebar {
  overflow: hidden;
}
.sidebar-section {
  padding: 15px 14px;
}
.section-heading {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 11px;
}
.section-title {
  margin: 0;
  color: var(--quark-ink);
  font-size: 15px;
  font-weight: 650;
  line-height: 1.4;
}
.section-count {
  display: grid;
  min-width: 22px;
  height: 22px;
  place-items: center;
  padding: 0 6px;
  color: #53615c;
  background: #f0f3f1;
  border-radius: 11px;
  font-size: 12px;
}
.share-list {
  display: flex;
  max-height: 360px;
  flex-direction: column;
  gap: 8px;
  overflow-y: auto;
  padding-right: 2px;
}
.share-card {
  min-width: 0;
  padding: 10px 10px 8px;
  background: #fff;
  border: 1px solid #e8eeeb;
  border-radius: 9px;
  transition:
    border-color 0.16s ease,
    background-color 0.16s ease;
}
.share-card.active {
  background: #f8fbf9;
  border-color: #a8c9bd;
}
.share-pick {
  display: flex;
  width: 100%;
  min-width: 0;
  align-items: center;
  gap: 8px;
  padding: 1px 0 4px;
  color: inherit;
  text-align: left;
  background: transparent;
  border: 0;
  cursor: pointer;
}
.share-pick:focus-visible,
.breadcrumb-link:focus-visible,
.node-name:focus-visible,
.clear-selection:focus-visible {
  outline: 2px solid var(--quark-primary);
  outline-offset: 2px;
  border-radius: 4px;
}
.share-mark {
  display: grid;
  width: 27px;
  height: 27px;
  flex: 0 0 27px;
  place-items: center;
  color: var(--quark-primary);
  background: #eff6f3;
  border-radius: 7px;
}
.share-main {
  display: flex;
  min-width: 0;
  flex: 1;
  flex-direction: column;
  gap: 3px;
}
.share-title,
.share-target {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.share-title {
  color: #26312e;
  font-size: 13px;
  font-weight: 600;
}
.share-target {
  color: #74817c;
  font-size: 12px;
}
.share-chevron {
  flex: 0 0 auto;
  color: #8c9893;
}
.share-card-meta {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  padding: 5px 0 1px 35px;
}
.share-card-meta :deep(.status-badge) {
  flex: 0 0 auto;
}
.share-checked {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 4px;
  overflow: hidden;
  color: #7a8581;
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.share-card-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 2px;
  margin-top: 3px;
}
.watch-control {
  display: flex;
  align-items: center;
  gap: 7px;
  margin-right: auto;
  color: #4f5d58;
  font-size: 12px;
}
.watch-control :deep(.el-switch) {
  --el-switch-on-color: var(--quark-primary);
}
.share-card-actions :deep(.el-button) {
  width: 30px;
  height: 30px;
  color: #67746f;
}
.share-card-actions :deep(.el-button:hover) {
  color: var(--quark-primary);
  background: var(--quark-primary-soft);
}
.share-error,
.node-error {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  margin: 7px 0 0;
  color: #a43c36;
  font-size: 12px;
  line-height: 1.45;
}
.share-error :deep(svg),
.node-error :deep(svg) {
  flex: 0 0 auto;
  margin-top: 1px;
}
.share-empty {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 13px 10px;
  color: #75817c;
  background: #f8faf9;
  border-radius: 8px;
  font-size: 13px;
  line-height: 1.45;
}
.sidebar-divider {
  height: 1px;
  background: var(--quark-line);
}
.tree-section {
  padding-bottom: 12px;
}
.tree-share-name {
  max-width: 138px;
  overflow: hidden;
  color: #79857f;
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.share-tree {
  --el-tree-node-hover-bg-color: #f3f7f5;
  --el-tree-text-color: #42504b;
  --el-tree-expand-icon-color: #76827d;
  color: #42504b;
}
.share-tree :deep(.el-tree-node__content) {
  height: 34px;
  padding-right: 5px;
  border-radius: 6px;
}
.share-tree :deep(.el-tree-node.is-current > .el-tree-node__content) {
  color: var(--quark-primary);
  background: var(--quark-primary-soft);
}
.tree-node-label {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 8px;
  font-size: 13px;
}
.tree-node-label svg {
  flex: 0 0 auto;
  color: #75827c;
}
.tree-node-label span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.tree-empty {
  padding: 8px 3px 4px;
  color: #7b8782;
  font-size: 13px;
  line-height: 1.5;
}
.node-error {
  padding: 0 2px;
}
.cloud-browser {
  display: flex;
  min-height: 540px;
  flex-direction: column;
  overflow: hidden;
}
.browser-topline {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding: 17px 18px 13px;
  border-bottom: 1px solid #eef1ef;
}
.browser-heading {
  min-width: 0;
}
.browser-kicker {
  display: block;
  margin-bottom: 4px;
  color: #75817c;
  font-size: 12px;
}
.browser-title-row {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 9px;
}
.browser-title-row .section-title {
  overflow: hidden;
  font-size: 17px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.browser-target {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 6px;
  margin: 6px 0 0;
  color: #7a8580;
  font-size: 12px;
}
.browser-target strong {
  overflow-wrap: anywhere;
  color: #4e5d57;
  font-size: 12px;
  font-weight: 550;
}
.browser-actions {
  justify-content: flex-end;
}
.browser-actions :deep(.el-button) {
  min-height: 34px;
  padding: 0 10px;
}
.share-issue {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin: 12px 18px 0;
  padding: 9px 11px;
  color: #8d4d20;
  background: #fff8ed;
  border: 1px solid #f1e1c8;
  border-radius: 7px;
  font-size: 13px;
  line-height: 1.45;
}
.share-issue svg {
  flex: 0 0 auto;
  margin-top: 1px;
}
.browser-toolbar {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  padding: 12px 16px;
}
.breadcrumbs {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 9px;
}
.breadcrumbs :deep(.el-button) {
  min-height: 32px;
  padding: 0 8px;
  color: #586761;
}
.breadcrumbs :deep(.el-button:hover:not(:disabled)) {
  color: var(--quark-primary);
}

.breadcrumbs :deep(.el-breadcrumb) {
  min-width: 0;
  overflow: hidden;
}
.breadcrumbs :deep(.el-breadcrumb__item:last-child .el-breadcrumb__inner) {
  max-width: 190px;
  overflow: hidden;
  color: #33413c;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.breadcrumb-link {
  max-width: 155px;
  overflow: hidden;
  padding: 1px 0;
  color: #63706b;
  font: inherit;
  text-overflow: ellipsis;
  white-space: nowrap;
  background: transparent;
  border: 0;
  cursor: pointer;
}
.breadcrumb-link:hover {
  color: var(--quark-primary);
}
.search-box {
  display: flex;
  width: min(370px, 48%);
  min-width: 250px;
  gap: 7px;
}
.search-box :deep(.el-input__wrapper),
.search-box :deep(.el-select__wrapper),
.share-dialog :deep(.el-input__wrapper),
.share-dialog :deep(.el-textarea__inner),
.share-dialog :deep(.el-select__wrapper) {
  box-shadow: 0 0 0 1px #dce4e0 inset;
}
.search-box :deep(.el-input__wrapper.is-focus),
.search-box :deep(.el-select__wrapper.is-focused),
.share-dialog :deep(.el-input__wrapper.is-focus),
.share-dialog :deep(.el-textarea__inner:focus),
.share-dialog :deep(.el-select__wrapper.is-focused) {
  box-shadow: 0 0 0 1px var(--quark-primary) inset;
}
.search-scope {
  width: 116px;
  flex: 0 0 116px;
}
.folder-changed {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 7px;
  margin: 0 16px 10px;
  padding: 8px 10px;
  color: #85551e;
  background: #fff9ef;
  border: 1px solid #f0e2c9;
  border-radius: 7px;
  font-size: 12px;
}
.folder-changed svg {
  flex: 0 0 auto;
}
.folder-changed :deep(.el-button) {
  min-height: 25px;
  padding: 0 4px;
}
.selection-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin: 0 16px 10px;
  padding: 9px 11px;
  background: #f1f8f5;
  border: 1px solid #d9ebe3;
  border-radius: 8px;
}
.selection-count {
  display: flex;
  align-items: center;
  gap: 7px;
  color: #30463e;
  font-size: 13px;
  font-weight: 550;
}
.selection-check {
  display: grid;
  min-width: 21px;
  height: 21px;
  place-items: center;
  padding: 0 5px;
  color: #fff;
  background: var(--quark-primary);
  border-radius: 11px;
  font-size: 12px;
}
.clear-selection {
  padding: 4px 5px;
  color: #64736d;
  font: inherit;
  background: transparent;
  border: 0;
  cursor: pointer;
}
.clear-selection:hover {
  color: var(--quark-primary);
}
.selection-bar :deep(.el-button) {
  min-height: 34px;
}
.table-viewport {
  min-width: 0;
  overflow-x: auto;
  padding: 0 12px;
}
.quark-table {
  min-width: 850px;
  width: 100%;
  --el-table-border-color: #eef1ef;
  --el-table-header-bg-color: #f7f9f8;
  --el-table-header-text-color: #64716c;
  --el-table-text-color: #36423e;
  --el-table-row-hover-bg-color: #f6faf8;
  --el-color-primary: var(--quark-primary);
}
.quark-table :deep(.el-table__header th) {
  height: 42px;
  font-size: 12px;
  font-weight: 650;
}
.quark-table :deep(.el-table__body td) {
  height: 48px;
  font-size: 13px;
}
.quark-table :deep(.el-table__cell) {
  padding: 0 5px;
}
.quark-table :deep(.is-selected-row td) {
  background: #f3faf6;
}

.node-name {
  display: flex;
  width: 100%;
  min-width: 0;
  align-items: center;
  gap: 9px;
  padding: 4px 0;
  border: 0;
  text-align: left;
  background: transparent;
}
.node-name > span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.folder-name {
  color: #34443d;
  font: inherit;
  cursor: pointer;
}
.folder-name svg:first-child {
  flex: 0 0 auto;
  color: #638978;
}
.folder-name svg:last-child {
  flex: 0 0 auto;
  margin-left: auto;
  color: #9aa59f;
}
.folder-name:hover {
  color: var(--quark-primary);
}
.file-name {
  color: #45524d;
}
.file-name svg {
  flex: 0 0 auto;
  color: #7e8b85;
}
.type-cell {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: #65726c;
  font-size: 12px;
}
.type-cell svg {
  color: #87938d;
}
.table-viewport :deep(.el-empty) {
  padding: 28px 0;
}
.table-viewport :deep(.el-empty__description p) {
  color: #89948f;
  font-size: 13px;
}
.table-footnote {
  display: flex;
  min-height: 40px;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 0 17px;
  color: #818c87;
  font-size: 12px;
}
.dialog-intro {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: -4px 0 17px;
  padding: 11px 12px;
  color: #4c6258;
  background: #f1f8f5;
  border: 1px solid #dcece5;
  border-radius: 8px;
}
.dialog-intro p {
  margin: 0;
  font-size: 13px;
  line-height: 1.5;
}
.dialog-icon {
  display: grid;
  width: 30px;
  height: 30px;
  flex: 0 0 30px;
  place-items: center;
  color: var(--quark-primary);
  background: #e0f0e9;
  border-radius: 8px;
}
.share-dialog :deep(.el-dialog) {
  max-width: 840px;
  border-radius: 12px;
}
.share-dialog :deep(.el-dialog__header) {
  margin-right: 0;
  padding: 20px 22px 12px;
}
.share-dialog :deep(.el-dialog__title) {
  color: var(--quark-ink);
  font-size: 17px;
  font-weight: 650;
}
.share-dialog :deep(.el-dialog__body) {
  padding: 8px 22px 14px;
}
.share-dialog :deep(.el-dialog__footer) {
  padding: 12px 22px 19px;
  border-top: 1px solid #edf0ee;
}

.share-dialog :deep(.el-form-item) {
  margin-bottom: 15px;
}
.share-dialog :deep(.el-form-item__label) {
  color: #3c4944;
  font-size: 13px;
  font-weight: 600;
}
.share-dialog :deep(.el-textarea__inner) {
  min-height: 112px !important;
  padding: 10px 11px;
  color: var(--quark-ink);
  font-size: 13px;
  line-height: 1.6;
}
.field-hint {
  margin-top: 6px;
  color: #75817c;
  font-size: 12px;
  line-height: 1.5;
}
.folder-mode-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin: 1px 0 13px;
}
.folder-mode-row > div {
  display: flex;
  flex-direction: column;
  gap: 3px;
}
.folder-mode-row strong {
  color: #3c4944;
  font-size: 13px;
}
.folder-mode-row span {
  color: #78837e;
  font-size: 12px;
}
.folder-mode-row :deep(.el-radio-button__inner) {
  min-height: 34px;
  padding: 9px 11px;
  color: #58655f;
  font-size: 12px;
  box-shadow: none;
}
.folder-mode-row
  :deep(.el-radio-button__original-radio:checked + .el-radio-button__inner) {
  color: var(--quark-primary);
  background: #eff7f3;
  border-color: #a9c8bc;
  box-shadow: -1px 0 0 0 #a9c8bc;
}
.folder-select {
  width: 100%;
}
.entry-target-list {
  display: flex;
  max-height: 280px;
  flex-direction: column;
  gap: 9px;
  overflow: auto;
  padding: 2px 2px 4px;
}
.entry-target-row {
  display: grid;
  grid-template-columns: minmax(200px, 1.1fr) minmax(240px, 0.9fr);
  align-items: center;
  gap: 12px;
  padding: 9px 10px;
  background: #fafcfb;
  border: 1px solid #e8eeeb;
  border-radius: 8px;
}
.entry-target-link {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 8px;
  color: #53615b;
  font-size: 12px;
}
.entry-target-link > span:last-child {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.entry-index {
  display: grid;
  width: 22px;
  height: 22px;
  flex: 0 0 22px;
  place-items: center;
  color: #53645c;
  background: #edf2ef;
  border-radius: 50%;
  font-size: 11px;
  font-weight: 600;
}
.entry-target-empty {
  padding: 13px;
  color: #808c86;
  background: #fafcfb;
  border: 1px dashed #dfe7e2;
  border-radius: 8px;
  text-align: center;
  font-size: 13px;
}

.form-error {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  margin: 8px 0 4px;
  padding: 9px 10px;
  color: #a13d37;
  background: #fff5f4;
  border: 1px solid #f0d5d2;
  border-radius: 7px;
  font-size: 13px;
  line-height: 1.45;
}
.form-error svg {
  flex: 0 0 auto;
  margin-top: 1px;
}
.create-result {
  margin-top: 12px;
  padding: 11px;
  color: #42534a;
  background: #f4f9f6;
  border: 1px solid #dcebe2;
  border-radius: 8px;
  font-size: 13px;
}
.create-result-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}
.create-result-heading > span {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  color: #286750;
  font-weight: 650;
}
.create-result-heading small {
  color: #78857e;
  font-size: 11px;
}
.create-result-list {
  display: flex;
  max-height: 190px;
  flex-direction: column;
  gap: 6px;
  overflow: auto;
  margin-top: 9px;
}
.create-result-row {
  display: grid;
  grid-template-columns: 22px minmax(100px, 0.75fr) minmax(180px, 1fr);
  align-items: center;
  gap: 8px;
  padding: 7px 8px;
  background: #fff;
  border: 1px solid #e6eee9;
  border-radius: 6px;
  color: #4e5b55;
  font-size: 12px;
}
.create-result-row.failed {
  color: #a13d37;
  background: #fff9f8;
  border-color: #f0dfdc;
}
.result-url {
  overflow: hidden;
  color: #68756f;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.create-result > p {
  margin: 8px 0 0;
  color: #63716a;
  font-size: 12px;
  line-height: 1.5;
}
.quark-page :deep(button:focus-visible),
.quark-page :deep(.el-button:focus-visible),
.quark-page :deep(.el-switch:focus-visible),
.quark-page :deep(.el-tree-node__content:focus-visible),
.quark-page :deep(.el-select__wrapper:focus-visible) {
  outline: 2px solid #166a59;
  outline-offset: 2px;
}

@media (max-width: 1160px) {
  .cloud-workspace {
    grid-template-columns: minmax(220px, 250px) minmax(0, 1fr);
  }
  .overview-item,
  .download-root {
    padding-right: 11px;
    padding-left: 11px;
  }
  .browser-toolbar {
    align-items: flex-start;
    flex-direction: column;
  }
  .search-box {
    width: 100%;
    min-width: 0;
  }
}
@media (max-width: 860px) {
  .quark-overview {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .download-root {
    grid-column: 1 / -1;
  }
  .cloud-workspace {
    grid-template-columns: minmax(0, 1fr);
  }
  .share-list {
    max-height: 250px;
  }
  .share-tree {
    max-height: 220px;
    overflow: auto;
  }
  .cloud-browser {
    min-height: 400px;
  }
}
@media (max-width: 620px) {
  .quark-header,
  .browser-topline {
    align-items: flex-start;
    flex-direction: column;
  }
  .header-actions,
  .browser-actions {
    width: 100%;
  }
  .header-actions :deep(.el-button),
  .browser-actions :deep(.el-button) {
    flex: 1;
  }
  .quark-overview {
    grid-template-columns: 1fr;
  }
  .download-root {
    grid-column: auto;
  }
  .browser-actions {
    justify-content: flex-start;
  }
  .browser-toolbar {
    padding: 10px 12px;
  }
  .breadcrumbs {
    width: 100%;
  }
  .search-box {
    flex-direction: column;
  }
  .search-scope {
    width: 100%;
    flex-basis: auto;
  }
  .entry-target-row {
    grid-template-columns: 1fr;
  }
  .folder-mode-row {
    align-items: flex-start;
    flex-direction: column;
  }
  .folder-mode-row :deep(.el-radio-group) {
    display: flex;
    width: 100%;
  }
  .folder-mode-row :deep(.el-radio-button) {
    flex: 1;
  }
  .folder-mode-row :deep(.el-radio-button__inner) {
    width: 100%;
    padding: 9px 6px;
  }
  .table-footnote {
    align-items: flex-start;
    flex-direction: column;
    justify-content: center;
    padding: 8px 15px;
  }
  .create-result-row {
    grid-template-columns: 22px minmax(0, 1fr);
  }
  .create-result-row > span:last-child {
    grid-column: 2;
  }
}
@media (prefers-reduced-motion: reduce) {
  .quark-page *,
  .quark-page *::before,
  .quark-page *::after {
    scroll-behavior: auto !important;
    transition-duration: 0.01ms !important;
    animation-duration: 0.01ms !important;
    animation-iteration-count: 1 !important;
  }
}
</style>
