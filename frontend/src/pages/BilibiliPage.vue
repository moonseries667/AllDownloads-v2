<script setup lang="ts">
import { ref, computed, watch, onMounted, onActivated, onBeforeUnmount } from "vue";
import { useRoute } from "vue-router";
import {
  Plus,
  RefreshCw,
  Download,
  Info,
  Search,
  LayoutGrid,
  List,
  Check,
} from "lucide-vue-next";
import { ElMessageBox } from "element-plus";
import { api, dataRevision, isDemo, notifyError, notifySuccess } from "../api/client";
import type { RecordData, Selection } from "../api/types";
import { formatDate, duration } from "../format";
import StatusBadge from "../components/StatusBadge.vue";
import SelectFilter from "../components/SelectFilter.vue";
import DownloadPreview from "../components/DownloadPreview.vue";
import BilibiliDetail from "../components/BilibiliDetail.vue";
import SourceNavigation from "../components/SourceNavigation.vue";
import PublishedDateFilter from "../components/PublishedDateFilter.vue";
const route = useRoute();
const contents = ref<RecordData[]>([]),
  sources = ref<RecordData[]>([]),
  source = ref(""),
  q = ref(""),
  type = ref(""),
  state = ref(""),
  sort = ref("PUBLISHED_DESC"),
  dateFrom = ref(""),
  dateTo = ref(""),
  page = ref(1),
  view = ref("grid"),
  loading = ref(false),
  error = ref("");
const selectedIds = ref<string[]>([]),
  querySelection = ref(false),
  frozenIds = ref<string[]>([]),
  exclude = ref<string[]>([]);
const addOpen = ref(false),
  links = ref(""),
  adding = ref(false),
  addResult = ref<RecordData[]>([]);
const detailId = ref<string | null>(null),
  detailOpen = ref(false),
  sourceOpen = ref(false),
  alias = ref(""),
  pinned = ref(false),
  editingSource = ref<RecordData | null>(null);
const sourceSaving = ref(false), sourceError = ref(""), sourceFeedback = ref("");
const platformSummary = ref<RecordData | null>(null), summaryError = ref("");
const summaryLabels: Record<string, string> = { ACTIVE: "可用", READY: "可用", AUTH_REQUIRED: "需要登录", NOT_CONFIGURED: "未配置", UNKNOWN: "待确认", RUNNING: "刷新中", QUEUED: "等待刷新", IDLE: "暂无刷新任务", NEEDS_ACTION: "需要处理", FAILED: "刷新失败" };
async function loadSummary() {
  try { platformSummary.value = (await api.get("/overview")).bilibili || null; summaryError.value = ""; }
  catch (e) { platformSummary.value = null; summaryError.value = (e as Error).message; }
}
const previewOpen = ref(false),
  previewSelection = ref<Selection>({
    mode: "EXPLICIT",
    resource: "BILIBILI_CONTENTS",
    ids: [],
  });
const filters = computed(() => ({
  sourceId: source.value,
  q: q.value,
  type: type.value,
  state: state.value,
  from: dateFrom.value,
  to: dateTo.value,
  sort: sort.value,
}));
const filtered = computed(() => contents.value);
const visible = computed(() =>
  filtered.value.slice((page.value - 1) * 6, page.value * 6),
);
const currentSource = computed(() =>
  sources.value.find((s) => s.id === source.value),
);
const selectionCount = computed(() =>
  querySelection.value
    ? frozenIds.value.length - exclude.value.length
    : selectedIds.value.length,
);
const filterData = [
  {
    title: "内容类型",
    key: "type",
    options: [
      { value: "", label: "全部" },
      { value: "SINGLE", label: "单视频" },
      { value: "MULTIPART", label: "多 P 视频" },
      { value: "SERIES", label: "番剧" },
      { value: "MOVIE", label: "电影" },
    ],
  },
  {
    title: "下载状态",
    key: "state",
    options: [
      { value: "", label: "全部" },
      { value: "NOT_REQUESTED", label: "未下载" },
      { value: "DOWNLOADING", label: "下载中" },
      { value: "PARTIAL", label: "部分完成" },
      { value: "VERIFIED", label: "已完成" },
    ],
  },
];
let loadRun = 0;
async function readPages(path: string, query: Record<string, string> = {}) {
  const rows: RecordData[] = [], seen = new Set<string>();
  let cursor = "";
  do {
    const params = new URLSearchParams(Object.entries({ ...query, pageSize: "200", cursor }).filter(([, value]) => value !== ""));
    const result = await api.get(path + "?" + params);
    rows.push(...result.items);
    cursor = result.nextCursor || "";
    if (cursor && seen.has(cursor)) throw new Error("列表分页游标重复，请刷新后重试");
    seen.add(cursor);
  } while (cursor);
  return rows;
}
async function load() {
  const run = ++loadRun;
  if (dateFrom.value && dateTo.value && dateFrom.value > dateTo.value) { loading.value = false; return; }
  loading.value = true;
  try {
    const [c, s] = await Promise.all([
      readPages("/bilibili/contents", { ...filters.value }),
      readPages("/bilibili/sources"),
    ]);
    if (run !== loadRun) return;
    contents.value = c;
    sources.value = s;
    page.value = Math.min(page.value, Math.max(1, Math.ceil(c.length / 6)));
    error.value = "";
  } catch (e) {
    if (run === loadRun) error.value = (e as Error).message;
  } finally {
    if (run === loadRun) loading.value = false;
  }
}
watch(dataRevision, load);
watch(dataRevision, loadSummary);
watch([source, type, state, dateFrom, dateTo, sort], () => { page.value = 1; load(); });
let searchTimer: ReturnType<typeof setTimeout>;
watch(q, () => { page.value = 1; clearTimeout(searchTimer); searchTimer = setTimeout(load, 180); });
onBeforeUnmount(() => clearTimeout(searchTimer));
watch(
  () => route.query.q,
  (value) => {
    if (route.path === "/bilibili") q.value = String(value || "");
  },
  { immediate: true },
);
onMounted(load);
onMounted(loadSummary);
let refreshed = false;
onActivated(async () => {
  if (refreshed) return;
  refreshed = true;
  try {
    const s = await api.get("/bilibili/sources");
    if (s.items.length)
      await api.post("/bilibili/sources/" + s.items[0].id + "/refresh", {
        force: false,
      });
  } catch (e) {
    error.value = (e as Error).message;
  }
});
function isSelected(id: string) {
  return querySelection.value
    ? frozenIds.value.includes(id) && !exclude.value.includes(id)
    : selectedIds.value.includes(id);
}
function toggle(id: string) {
  if (querySelection.value) {
    if (!frozenIds.value.includes(id)) {
      selectedIds.value = [...frozenIds.value.filter(x => !exclude.value.includes(x)), id];
      querySelection.value = false;
      return;
    }
    exclude.value = isSelected(id)
      ? [...exclude.value, id]
      : exclude.value.filter((x) => x !== id);
  } else
    selectedIds.value = isSelected(id)
      ? selectedIds.value.filter((x) => x !== id)
      : [...selectedIds.value, id];
}
function addSelection(id: string) {
  if (isSelected(id)) return;
  if (querySelection.value && !frozenIds.value.includes(id)) {
    selectedIds.value = frozenIds.value.filter(x => !exclude.value.includes(x));
    querySelection.value = false;
  }
  toggle(id);
}
function clear() {
  selectedIds.value = [];
  querySelection.value = false;
  frozenIds.value = [];
  exclude.value = [];
}
function selectAll() {
  querySelection.value = true;
  frozenIds.value = filtered.value.map((c) => c.id);
  exclude.value = [];
  selectedIds.value = [];
}
function openPreview(id?: string) {
  previewSelection.value = id
    ? { mode: "EXPLICIT", resource: "BILIBILI_CONTENTS", ids: [id] }
    : querySelection.value
      ? {
          mode: "EXPLICIT",
          resource: "BILIBILI_CONTENTS",
          ids: frozenIds.value.filter(id => !exclude.value.includes(id)),
        }
      : {
          mode: "EXPLICIT",
          resource: "BILIBILI_CONTENTS",
          ids: [...selectedIds.value],
        };
  previewOpen.value = true;
}
function showDetail(c: RecordData) {
  detailId.value = c.id;
  detailOpen.value = true;
}
function cardDuration(c: RecordData) {
  const seconds = c.durationSeconds ?? (c.mediaUnits?.length === 1 ? c.mediaUnits[0].durationSeconds : null);
  return duration(seconds);
}
function unitCount(c: RecordData) {
  return c.mediaUnits?.length ?? c.downloadSummary.totalCount ?? 0;
}
async function addSources() {
  adding.value = true;
  try {
    const input = links.value.split(/\s+/).filter(Boolean);
    if (!input.length) throw new Error("请输入至少一条来源链接");
    addResult.value = (
      await api.post("/bilibili/sources", { links: input })
    ).items;
    notifySuccess("来源解析请求已受理，仅收录信息。");
  } catch (e) {
    notifyError(e);
  } finally {
    adding.value = false;
  }
}
async function refresh() {
  try {
    const ids = source.value ? [source.value] : sources.value.map((s) => s.id);
    for (const id of ids)
      await api.post("/bilibili/sources/" + id + "/refresh", { force: true });
    notifySuccess("已申请刷新来源信息；没有创建下载订单。");
  } catch (e) {
    notifyError(e);
  }
}
function editSource(s: RecordData) {
  editingSource.value = s;
  alias.value = s.alias;
  pinned.value = s.pinned;
  sourceOpen.value = true;
  sourceError.value = "";
  sourceFeedback.value = "";
}
async function persistSource(changes: RecordData) {
  if (sourceSaving.value || !editingSource.value) return false;
  sourceSaving.value = true;
  sourceError.value = "";
  sourceFeedback.value = "";
  try {
    const updated = await api.patch(
      "/bilibili/sources/" + editingSource.value!.id,
      changes,
      editingSource.value!.revision,
    );
    editingSource.value = updated;
    pinned.value = updated.pinned;
    sources.value = sources.value.map(s => s.id === updated.id ? updated : s);
    return true;
  } catch (e) {
    sourceError.value = (e as Error).message;
    // Re-read the persisted value after conflicts or a lost response.
    try {
      const latest = (await readPages("/bilibili/sources")).find(s => s.id === editingSource.value!.id);
      if (latest) { editingSource.value = latest; pinned.value = latest.pinned; }
    } catch { pinned.value = editingSource.value!.pinned; }
    notifyError(e);
    return false;
  } finally {
    sourceSaving.value = false;
  }
}
async function changePinned(value: string | number | boolean) {
  if (await persistSource({ pinned: Boolean(value) })) sourceFeedback.value = pinned.value ? "已置顶，刷新后保留" : "已取消置顶，刷新后保留";
}
async function saveSource() {
  if (await persistSource({ alias: alias.value })) {
    sourceOpen.value = false;
    notifySuccess("来源显示设置已更新");
  }
}
async function removeSource() {
  try {
    await ElMessageBox.confirm(
      "仅移除来源关系，保留已下载资料、未结束任务及其他来源引用。",
      "移除来源",
      {
        confirmButtonText: "移除来源",
        cancelButtonText: "取消",
        type: "warning",
      },
    );
    await api.delete(
      "/bilibili/sources/" + editingSource.value!.id,
      editingSource.value!.revision,
    );
    sourceOpen.value = false;
    source.value = "";
    notifySuccess("来源关系已移除，已下载内容保留");
  } catch (e) {
    if (e !== "cancel" && e !== "close") notifyError(e);
  }
}
</script>
<template>
  <div class="bilibili-page">
    <div class="page-header">
      <div>
        <h1 class="page-title bilibili-heading">Bilibili <span v-if="isDemo" class="demo-indicator">隔离演示</span></h1>
        <div class="platform-summary" aria-label="Bilibili 摘要" aria-live="polite">
          <span>账号 <strong>{{ summaryLabels[platformSummary?.accountState] || '待确认' }}</strong></span>
          <span>资料 <strong>{{ summaryLabels[platformSummary?.refreshState] || '状态未知' }}</strong></span>
          <span>已下载 <strong>{{ platformSummary?.downloadCount ?? '—' }}</strong></span>
          <span>已归档 <strong>{{ platformSummary?.archiveCount ?? '—' }}</strong></span>
        </div>
        <p v-if="summaryError" class="text-danger" role="status">摘要暂不可用：{{ summaryError }}</p>
      </div>
      <div class="toolbar">
        <el-button :icon="RefreshCw" @click="refresh">刷新信息</el-button
        ><el-button
          type="primary"
          :icon="Plus"
          @click="
            addOpen = true;
            addResult = [];
          "
          >添加来源</el-button
        >
      </div>
    </div>
    <el-alert
      v-if="error"
      :title="error"
      type="error"
      show-icon
      :closable="false"
    />
    <div class="library-layout">
      <SourceNavigation v-model="source" :sources="sources" @edit="editSource" />
      <section class="library-content">
        <div class="content-toolbar">
          <h2>
            {{ currentSource?.alias || currentSource?.title || "全部内容" }}
            <span>{{ filtered.length }}</span>
          </h2>
          <div class="toolbar">
            <el-input
              v-model="q"
              placeholder="搜索视频标题"
              aria-label="搜索视频标题"
              clearable
              class="search-field"
              ><template #prefix><Search :size="16" /></template
            ></el-input>
            <div class="view-toggle">
              <button
                aria-label="卡片视图"
                :class="{ active: view === 'grid' }"
                @click="view = 'grid'"
              >
                <LayoutGrid :size="17" /></button
              ><button
                aria-label="列表视图"
                :class="{ active: view === 'list' }"
                @click="view = 'list'"
              >
                <List :size="17" />
              </button>
            </div>
          </div>
        </div>
        <SelectFilter
          :data="filterData"
          :default-values="{ type, state }"
          @change="
            type = $event.type;
            state = $event.state;
          "
        />
        <div class="filter-secondary">
          <PublishedDateFilter v-model:from="dateFrom" v-model:to="dateTo" />
          <el-select v-model="sort" aria-label="排序方式" class="sort-select">
            <el-option label="标题正序" value="TITLE_ASC" />
            <el-option label="标题逆序" value="TITLE_DESC" />
            <el-option label="最新视频" value="PUBLISHED_DESC" />
            <el-option label="最早视频" value="PUBLISHED_ASC" />
          </el-select>
        </div>
        <div class="library-meta">
          <span
            >{{
              currentSource?.status === "NEEDS_ACTION"
                ? "来源刷新失败，保留已有信息"
                : "信息独立更新"
            }}
            ·
            {{
              formatDate(currentSource?.lastSyncedAt || contents[0]?.updatedAt)
            }}</span
          ><el-button
            link
            type="primary"
            @click="selectAll"
            :disabled="!filtered.length || loading || !!error || (!!dateFrom && !!dateTo && dateFrom > dateTo)"
            >全选筛选结果（{{ filtered.length }}）</el-button
          >
        </div>
        <div
          v-loading="loading"
          class="video-grid"
          :class="{ 'video-list': view === 'list' }"
        >
          <article
            v-for="c in visible"
            :key="c.id"
            class="video-card"
            :class="{ chosen: isSelected(c.id) }"
            @click="addSelection(c.id)"
          >
            <div class="video-cover">
              <img v-if="c.coverUrl" :src="c.coverUrl" :alt="c.title + '封面'" />
              <span v-else class="cover-placeholder">暂无封面</span>
              <button
                class="cover-checkbox"
                :aria-label="'选择 ' + c.title"
                :aria-pressed="isSelected(c.id)"
                @click.stop="toggle(c.id)"
              >
                <Check v-if="isSelected(c.id)" :size="15" /></button
              ><span class="video-duration">{{
                cardDuration(c)
              }}</span
              ><span class="part-tag" v-if="unitCount(c) > 1"
                >{{ c.kind === "SERIES" ? "剧集" : c.kind === "MOVIE" ? "版本" : "多 P" }} ·
                {{ unitCount(c) }}</span
              >
            </div>
            <div class="video-info">
              <a
                class="video-title"
                @click.stop
                :href="c.originalUrl"
                target="_blank"
                rel="noopener noreferrer"
                >{{ c.title }}</a
              >
              <div class="video-author">
                {{ c.author?.name || '作者未知'
                }}<span>{{
                  sources.find((s) => c.sourceIds.includes(s.id))?.alias ||
                  sources.find((s) => c.sourceIds.includes(s.id))?.title ||
                  "暂无有效来源"
                }}</span>
              </div>
              <p class="video-published">发布于 {{ c.publishedAt ? formatDate(c.publishedAt).slice(0, 10) : '未知日期' }}</p>
              <div class="video-status">
                <StatusBadge :state="c.downloadSummary.state" :label="c.downloadSummary.state === 'VERIFIED' ? '已下载' : undefined" /><span
                  v-if="c.archiveSummary.state !== 'NOT_REQUESTED'"
                  class="archive-mini"
                  >云端：<StatusBadge :state="c.archiveSummary.state" :label="c.archiveSummary.state === 'VERIFIED' ? '已归档' : undefined"
                /></span>
              </div>
              <div class="video-actions">
                <el-button link :icon="Info" @click.stop="showDetail(c)"
                  >详情</el-button
                ><el-button
                  link
                  type="primary"
                  :icon="Download"
                  @click.stop="openPreview(c.id)"
                  >下载</el-button
                >
              </div>
            </div>
          </article>
        </div>
        <el-empty
          v-if="!loading && !visible.length"
          description="暂无匹配内容，试试调整筛选或添加来源"
          :image-size="90"
        />
        <div class="pagination-row">
          <span class="muted">每页 6 项 · 全部内容按内容 ID 去重</span
          ><el-pagination
            v-model:current-page="page"
            :page-size="6"
            :total="filtered.length"
            layout="prev, pager, next"
          />
        </div>
        <div v-if="selectionCount" class="selection-bar">
          <div>
            <strong>已选择 {{ selectionCount }} 项</strong
            ><span>{{
              querySelection
                ? "筛选范围已固定，取消勾选可排除项目"
                : "跨页选择已保留"
            }}</span>
          </div>
          <div class="toolbar">
            <el-button text @click="clear">清空选择</el-button
            ><el-button type="primary" :icon="Download" @click="openPreview()"
              >预览下载清单</el-button
            >
          </div>
        </div>
      </section>
    </div>
    <el-dialog v-model="addOpen" title="添加 B 站信息来源" width="660px"
      ><p class="dialog-intro">
        支持收藏夹、合集、单视频、番剧和电影。每行一条链接，只收录信息。
      </p>
      <label class="field-label" for="b-links">来源链接</label
      ><el-input
        id="b-links"
        v-model="links"
        type="textarea"
        :rows="5"
        placeholder="粘贴 B 站链接，每行一条"
      />
      <div v-if="addResult.length" class="result-list">
        <div v-for="(r, i) in addResult" :key="i">
          <span class="break-anywhere">{{ r.url }}</span
          ><strong :class="r.error ? 'text-danger' : 'text-primary'">{{
            r.error?.message || (r.reused ? "来源已存在" : "解析已排队")
          }}</strong>
        </div>
      </div>
      <template #footer
        ><el-button @click="addOpen = false">关闭</el-button
        ><el-button type="primary" :loading="adding" @click="addSources"
          >加入信息来源</el-button
        ></template
      ></el-dialog
    >
    <el-dialog v-model="sourceOpen" title="管理来源" width="490px" :close-on-click-modal="!sourceSaving" :close-on-press-escape="!sourceSaving" :show-close="!sourceSaving"
      ><label class="field-label">显示别名</label
      ><el-input v-model="alias" :disabled="sourceSaving" placeholder="保留原名称时留空" />
      <div class="setting-inline">
        <span>置顶来源</span><el-switch :model-value="pinned" :loading="sourceSaving" :disabled="sourceSaving" aria-label="置顶来源" @change="changePinned" />
      </div>
      <p v-if="sourceSaving" role="status">正在保存来源设置…</p>
      <p v-if="sourceFeedback" class="text-primary" role="status">{{ sourceFeedback }}</p>
      <p v-if="sourceError" class="text-danger" role="alert">{{ sourceError }}</p>
      <p class="muted">置顶立即保存，别名点击保存后生效。</p>
      <template #footer><div class="source-dialog-actions">
        <el-button type="danger" plain :disabled="sourceSaving" @click="removeSource"
          >移除来源</el-button
        ><el-button :disabled="sourceSaving" @click="sourceOpen = false">取消</el-button
        ><el-button type="primary" :loading="sourceSaving" @click="saveSource">保存</el-button></div></template
      ></el-dialog
    >
    <BilibiliDetail v-model="detailOpen" :content-id="detailId" @saved="notifySuccess('选定内容已保存；下载清单将使用这些选项。')" />
    <DownloadPreview
      v-model="previewOpen"
      platform="BILIBILI"
      :selection="previewSelection"
    />
  </div>
</template>
