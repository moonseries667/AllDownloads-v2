<script setup lang="ts">
import { computed, inject, onActivated, onBeforeUnmount, onDeactivated, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { ArrowUpRight, Search } from "lucide-vue-next";
import { dataRevision, notifyError } from "../api/client";
import { downloadManagementApi } from "../api/download-management";
import { cancelReason, cancelling, downloadManagementKey, finished, type DownloadControl, type DownloadDetail, type DownloadFilters, type DownloadPart, type DownloadProject } from "../download-management";
import { formatDate, platformName } from "../format";
import StatusBadge from "../components/StatusBadge.vue";
import DownloadTable from "../components/DownloadTable.vue";

const service = inject(downloadManagementKey, downloadManagementApi);
const route = useRoute(), router = useRouter();
const projects = ref<DownloadProject[]>([]), detail = ref<DownloadDetail | null>(null);
const open = ref(false), loading = ref(false), detailLoading = ref(false), loadingMore = ref(false);
const error = ref(""), detailError = ref(""), actionError = ref("");
const nextCursor = ref<string | null>(null), total = ref<number | null>(null), search = ref("");
const pending = ref<Record<string, "sending" | "accepted">>({});
const sortKey = ref("date"), sortDirection = ref<"asc" | "desc">("desc");
const queryValue = (key: string) => typeof route.query[key] === "string" ? route.query[key] as string : "";
const filters = computed<DownloadFilters>(() => ({ platform: queryValue("platform"), contentId: queryValue("contentId"), sourceId: queryValue("sourceId"), state: queryValue("state"), q: queryValue("q") }));
const sorted = computed(() => projects.value.filter(project => (!filters.value.state || project.state === filters.value.state) && (!filters.value.q || project.title.toLocaleLowerCase().includes(filters.value.q.toLocaleLowerCase()))).sort((a, b) => {
  const value = (p: DownloadProject): string | number => sortKey.value === "title" ? p.title : sortKey.value === "platform" ? p.platform : sortKey.value === "state" ? p.state : sortKey.value === "progress" ? p.completedCount / Math.max(p.totalCount, 1) : p.createdAt;
  const av = value(a), bv = value(b);
  const compare = typeof av === "number" && typeof bv === "number" ? av - bv : String(av).localeCompare(String(bv), "zh-CN", { numeric: true });
  return (sortDirection.value === "asc" ? 1 : -1) * (compare || a.id.localeCompare(b.id));
}));
const columns = [
  { key: "title", label: "下载内容", min: 240, width: 340, sortable: true },
  { key: "platform", label: "平台", min: 112, width: 120, sortable: true },
  { key: "progress", label: "完成范围", min: 200, width: 210, sortable: true },
  { key: "state", label: "状态", min: 140, width: 140, sortable: true },
  { key: "date", label: "创建时间", min: 190, width: 190, sortable: true },
  { key: "actions", label: "操作", min: 238, width: 238 },
];
let active = false, listGeneration = 0, detailGeneration = 0, pageCount = 1;
let timer: ReturnType<typeof setTimeout> | undefined;
let detailId = "";
function controlKey(id: string, partId?: string) { return JSON.stringify([id, partId ?? null]); }
function busy(control: DownloadControl, projectId?: string) { return !!pending.value[controlKey(projectId ?? control.id, projectId === undefined ? undefined : control.id)]; }
function stopping(control: DownloadControl, projectId?: string) { return busy(control, projectId) || cancelling(control); }
function reason(control: DownloadControl, projectId?: string) { return busy(control, projectId) ? "取消请求正在处理；已完成文件会保留。" : cancelReason(control); }
function reconcile(project: DownloadProject | DownloadDetail) {
  const clear = (control: DownloadControl, key: string) => { if (finished(control) || control.desiredAction === "CANCEL") delete pending.value[key]; };
  clear(project, controlKey(project.id));
  if ("parts" in project) for (const part of project.parts) clear(part, controlKey(project.id, part.id));
}
function schedule() {
  clearTimeout(timer);
  if (!active) return;
  const running = projects.value.some(p => !finished(p)) || Object.keys(pending.value).length > 0;
  timer = setTimeout(() => void refresh(), document.hidden ? 15000 : running ? 2000 : 10000);
}
async function loadList(reset = false) {
  const generation = ++listGeneration;
  if (reset) { pageCount = 1; projects.value = []; nextCursor.value = null; total.value = null; }
  loading.value = true;
  const snapshot = { ...filters.value };
  try {
    let cursor: string | undefined;
    const rows: DownloadProject[] = [], seenCursors = new Set<string>();
    let resultTotal: number | null = null;
    for (let i = 0; i < pageCount; i++) {
      const page = await service.list(snapshot, cursor);
      if (generation !== listGeneration || !active) return;
      rows.push(...page.items); resultTotal = page.total;
      cursor = page.nextCursor ?? undefined;
      if (!cursor) break;
      if (seenCursors.has(cursor)) throw new Error("下载列表分页游标重复，请刷新后重试。");
      seenCursors.add(cursor);
    }
    projects.value = [...new Map(rows.map(p => [p.id, p])).values()];
    nextCursor.value = cursor ?? null; total.value = resultTotal;
    projects.value.forEach(reconcile); error.value = "";
  } catch (cause) {
    if (generation === listGeneration) { error.value = cause instanceof Error ? cause.message : "下载列表加载失败"; notifyError(cause); }
  } finally { if (generation === listGeneration) { loading.value = false; loadingMore.value = false; } }
}
async function loadDetail() {
  if (!open.value || !detailId) return;
  const id = detailId, generation = ++detailGeneration;
  detailLoading.value = true;
  try {
    const result = await service.detail(id);
    if (generation !== detailGeneration || !open.value || !active) return;
    detail.value = result; reconcile(result); detailError.value = "";
  } catch (cause) { if (generation === detailGeneration) { detailError.value = cause instanceof Error ? cause.message : "明细加载失败"; notifyError(cause); } }
  finally { if (generation === detailGeneration) detailLoading.value = false; }
}
async function refresh(reset = false) {
  clearTimeout(timer);
  await Promise.all([loadList(reset), loadDetail()]);
  schedule();
}
function more() { if (!nextCursor.value || loading.value) return; pageCount++; loadingMore.value = true; void refresh(); }
function show(project: DownloadProject) {
  detailId = project.id; detail.value = null; detailError.value = ""; actionError.value = ""; open.value = true; void loadDetail();
}
async function cancel(project: DownloadProject, part?: DownloadPart) {
  const control = part ?? project, key = controlKey(project.id, part?.id);
  if (pending.value[key] || cancelling(control) || !control.allowedActions.includes("CANCEL")) return;
  pending.value[key] = "sending"; actionError.value = "";
  try {
    if (part) await service.cancelPart(project.id, part); else await service.cancelProject(project);
    pending.value[key] = "accepted";
  } catch (cause) {
    delete pending.value[key];
    actionError.value = (cause instanceof Error ? cause.message : "取消请求未能确认") + "。已重新读取状态，请按最新状态重试。";
    notifyError(cause);
  }
  // Acknowledgement never changes a row to CANCELLED. Read the execution facts.
  if (active) await refresh();
}
function updateQuery(values: Record<string, string | undefined>) {
  const query = { ...route.query, ...values };
  for (const key of Object.keys(query)) if (query[key] === "" || query[key] === undefined) delete query[key];
  void router.push({ path: "/downloads", query });
}
function sort(key: string) { sortDirection.value = sortKey.value === key && sortDirection.value === "asc" ? "desc" : "asc"; sortKey.value = key; }
function start() { if (!active) { active = true; void refresh(); } }
function stop() { active = false; open.value = false; clearTimeout(timer); listGeneration++; detailGeneration++; }
watch(() => queryValue("q"), value => search.value = value, { immediate: true });
let currentScope = "";
watch(() => route.fullPath, () => {
  if (route.path !== "/downloads") return;
  const scope = JSON.stringify([filters.value.platform, filters.value.contentId, filters.value.sourceId]);
  if (scope === currentScope) return;
  currentScope = scope; open.value = false; actionError.value = ""; pageCount = 1; projects.value = []; nextCursor.value = null; total.value = null;
  if (active) void refresh(true);
}, { immediate: true });
watch(dataRevision, () => { if (active) void refresh(); });
watch(open, value => { if (!value) { detailGeneration++; detailId = ""; detail.value = null; } });
onMounted(start); onActivated(start); onDeactivated(stop); onBeforeUnmount(stop);
</script>
<template>
  <div class="download-management">
    <div class="page-header"><div><div class="page-eyebrow">传输中心</div><h1 class="page-title">下载管理</h1>
      <p class="page-subtitle">Bilibili 按视频、Telegram 按消息、夸克按分享展示；每次下载单独记录。</p></div>
      <el-button :icon="ArrowUpRight" @click="router.push('/tasks')">查看执行任务</el-button>
    </div>
    <div v-if="filters.contentId || filters.sourceId" class="scope-banner" role="status">
      <div><strong>{{ filters.contentId ? '当前内容的全部下载记录' : '当前来源的下载记录' }}</strong><span>{{ platformName[filters.platform || ''] || filters.platform }} · {{ filters.contentId || filters.sourceId }}</span></div>
      <el-button @click="updateQuery({ contentId: undefined, sourceId: undefined })">清除定向筛选</el-button>
    </div>
    <el-alert v-if="error" :title="'刷新未完成：' + error" description="已显示的数据可能过期。可刷新重试；筛选范围保持不变。" type="error" :closable="false" show-icon />
    <el-alert v-if="actionError" :title="actionError" type="error" :closable="false" show-icon />
    <div class="panel">
      <div class="panel-heading"><h2>下载项目 <span class="count-badge">{{ total ?? projects.length }}</span></h2>
        <div class="list-tools"><el-input v-model="search" clearable placeholder="在已加载项目中搜索" aria-label="搜索下载内容" @change="updateQuery({ q: search })"><template #prefix><Search :size="16" /></template></el-input>
          <el-button :loading="loading && !loadingMore" @click="refresh()">刷新</el-button></div>
      </div>
      <div class="download-filters">
        <label>平台 <select :value="filters.platform" aria-label="下载平台" @change="updateQuery({ platform: ($event.target as HTMLSelectElement).value, contentId: undefined })"><option value="">全部</option><option value="BILIBILI">Bilibili</option><option value="TELEGRAM">Telegram</option><option value="QUARK">夸克</option></select></label>
        <label>状态 <select :value="filters.state" aria-label="下载状态" @change="updateQuery({ state: ($event.target as HTMLSelectElement).value })"><option value="">全部</option><option value="QUEUED">等待中</option><option value="RUNNING">进行中</option><option value="SUCCEEDED">已完成</option><option value="PARTIAL">部分完成</option><option value="CANCELLED">已取消</option><option value="FAILED">失败</option><option value="NEEDS_ACTION">需要处理</option></select></label>
        <span>状态、标题与排序作用于已加载的 {{ projects.length }} 项；显示 {{ sorted.length }} 项</span>
      </div>
      <DownloadTable :rows="sorted" :columns="columns" :loading="loading" :sort-key="sortKey" :sort-direction="sortDirection" @sort="sort">
        <template #title="{ row }"><div class="project-title"><button type="button" class="title-link" :title="row.title" @click="show(row)">{{ row.title }}</button><small :title="'下载实例 ' + row.id">实例 {{ row.id }}</small><small :title="'提交批次 ' + row.batchId">批次 {{ row.batchId }}</small></div></template>
        <template #platform="{ row }">{{ platformName[row.platform] }}</template>
        <template #progress="{ row }"><div class="progress-text">已完成 {{ row.completedCount }} / {{ row.totalCount }}</div><el-progress :percentage="row.totalCount ? Math.min(100, Math.round(row.completedCount / row.totalCount * 100)) : 0" :show-text="false" :stroke-width="5" /><small>失败 {{ row.failedCount }} · 取消 {{ row.cancelledCount }}</small></template>
        <template #state="{ row }"><StatusBadge :state="row.state" :label="stopping(row) ? '正在取消' : undefined" /></template>
        <template #date="{ row }"><span class="mono-small">{{ formatDate(row.createdAt) }}</span></template>
        <template #actions="{ row }"><div class="row-actions"><el-button link type="primary" @click="show(row)">查看详情</el-button><el-button size="small" :disabled="stopping(row) || !row.allowedActions.includes('CANCEL')" :title="reason(row) || '取消此下载实例中尚未结束的部分，保留已完成文件'" @click="cancel(row)">{{ stopping(row) ? '正在取消' : '取消任务' }}</el-button></div><small v-if="reason(row)" class="cancel-reason" :title="reason(row)">{{ reason(row) }}</small></template>
        <template #empty>{{ loading ? '正在加载下载记录…' : error ? '下载记录暂时无法读取，请刷新重试。' : filters.contentId ? '此内容暂无下载记录。查看记录不会发起下载。' : '当前筛选范围暂无下载项目。' }}</template>
      </DownloadTable>
      <div v-if="nextCursor" class="pagination"><el-button :loading="loadingMore" :disabled="loading && !loadingMore" @click="more">加载更多下载项目</el-button></div>
    </div>
    <p class="muted">取消只停止当前下载实例尚未结束的范围，已完成文件保留。上传进度在「归档管理」单独展示。</p>
    <el-drawer v-model="open" title="下载项目详情" size="min(760px, 92vw)">
      <p v-if="detailLoading && !detail" role="status">正在加载明细…</p>
      <el-alert v-if="detailError" :title="detailError" description="当前明细可能过期，请刷新后查看。" type="error" :closable="false" />
      <el-button v-if="detailError" @click="loadDetail">刷新明细</el-button>
      <el-alert v-if="actionError" :title="actionError" type="error" :closable="false" />
      <template v-if="detail">
        <h2 class="detail-title">{{ detail.title }}</h2><StatusBadge :state="detail.state" :label="stopping(detail) ? '正在取消' : undefined" />
        <dl class="project-facts"><dt>平台</dt><dd>{{ platformName[detail.platform] }}</dd><dt>内容标识</dt><dd>{{ detail.contentId }}</dd><dt>下载实例</dt><dd>{{ detail.id }}</dd><dt>提交批次</dt><dd>{{ detail.batchId }}</dd></dl>
        <div class="detail-summary"><span>已完成 {{ detail.completedCount }} / {{ detail.totalCount }} · 失败 {{ detail.failedCount }} · 取消 {{ detail.cancelledCount }}</span>
          <el-button :disabled="stopping(detail) || !detail.allowedActions.includes('CANCEL')" @click="cancel(detail)">{{ stopping(detail) ? '正在取消' : '取消任务' }}</el-button></div>
        <p v-if="reason(detail)" class="muted">{{ reason(detail) }}</p>
        <h3>本次选定范围</h3><p class="muted">取消项仍计入原定范围；取消受理后需等待执行者确认。</p>
        <section v-for="part in detail.parts" :key="part.id" :data-part-id="part.id" class="download-part">
          <div class="part-heading"><h4>{{ part.title }}</h4><StatusBadge :state="part.state" :label="stopping(part, detail.id) ? '正在取消' : undefined" /></div>
          <p v-if="part.relativePath" class="path-text">{{ part.relativePath }}</p><small>{{ part.kind }} · {{ part.id }} · 已完成 {{ part.completedCount }} / {{ part.totalCount }}</small>
          <ul class="part-files"><li v-for="file in part.files" :key="file.id"><span>{{ file.relativePath }}</span><StatusBadge :state="file.state" /></li></ul>
          <p v-if="part.cancelImpact && (part.allowedActions.includes('CANCEL') || stopping(part, detail.id))" class="impact">{{ part.cancelImpact }}</p>
          <div class="part-footer"><span>{{ reason(part, detail.id) || '仅取消此部分，其他无关部分继续。' }}</span><el-button size="small" :disabled="stopping(part, detail.id) || !part.allowedActions.includes('CANCEL') || stopping(detail)" @click="cancel(detail, part)">{{ stopping(part, detail.id) ? '正在取消' : detail.platform === 'BILIBILI' ? '取消此部分' : '取消此文件' }}</el-button></div>
        </section>
        <p v-if="!detail.parts.length" class="muted">暂无可显示的执行明细。</p>
      </template>
    </el-drawer>
  </div>
</template>
<style scoped>
.download-management { min-width: 0; }
.scope-banner { display: flex; align-items: center; justify-content: space-between; gap: 20px; padding: 16px 20px; margin-bottom: 20px; background: var(--el-color-primary-light-9); border: 1px solid var(--el-color-primary-light-7); border-radius: 8px; }
.scope-banner span { display: block; margin-top: 4px; overflow-wrap: anywhere; }
.download-management > .el-alert { margin-bottom: 16px; }
.list-tools { display: flex; gap: 12px; align-items: center; }
.list-tools .el-input { width: 240px; }
.download-filters { display: flex; align-items: center; flex-wrap: wrap; gap: 16px; padding: 12px 20px; border-bottom: 1px solid var(--line); }
.download-filters label { display: flex; align-items: center; gap: 10px; }
.download-filters select { font: inherit; background: var(--el-bg-color); color: var(--text); padding: 7px 26px 7px 10px; border: 1px solid var(--line); border-radius: 5px; }
.download-filters > span { margin-left: auto; font-size: 13px; color: var(--muted); }
.project-title { display: grid; gap: 5px; }
.title-link { padding: 0; border: 0; background: none; font: inherit; font-weight: 600; text-align: left; color: var(--text); cursor: pointer; }
.title-link:hover { color: var(--primary); text-decoration: underline; }
small { color: var(--muted); font-size: 12px; }
.progress-text { min-width: 155px; margin-bottom: 8px; }
.row-actions { display: flex; align-items: center; gap: 12px; }
.row-actions :deep(.el-button + .el-button) { margin-left: 0; }
.cancel-reason { display: block; max-width: 198px; white-space: normal; margin-top: 5px; line-height: 1.5; }
.pagination { display: flex; justify-content: center; padding: 16px; }
.detail-title { overflow-wrap: anywhere; }
.project-facts { display: grid; grid-template-columns: 80px minmax(0, 1fr); gap: 10px; margin: 22px 0; }
.project-facts dt { color: var(--muted); }.project-facts dd { margin: 0; overflow-wrap: anywhere; }
.detail-summary { display: flex; gap: 16px; justify-content: space-between; align-items: center; flex-wrap: wrap; }
.download-part { border: 1px solid var(--line); border-radius: 8px; padding: 16px; margin: 12px 0; }
.part-heading { display: flex; align-items: baseline; justify-content: space-between; gap: 16px; }
.part-heading h4 { margin: 0; overflow-wrap: anywhere; min-width: 0; }
.part-heading :deep(.status-badge) { flex-shrink: 0; }
.impact { color: var(--el-color-warning-dark-2); font-size: 13px; line-height: 1.6; }
.part-footer { display: flex; justify-content: space-between; align-items: center; gap: 18px; margin-top: 14px; }
.part-footer span { color: var(--muted); font-size: 13px; line-height: 1.6; }
.part-footer .el-button { flex-shrink: 0; }
.part-files { padding: 0; margin: 14px 0; list-style: none; }
.part-files li { display: flex; justify-content: space-between; gap: 16px; align-items: baseline; padding: 5px 0; }
.part-files span:first-child { overflow-wrap: anywhere; min-width: 0; font-size: 13px; }
.part-files :deep(.status-badge) { flex-shrink: 0; }
button:focus-visible, select:focus-visible { outline: 2px solid var(--primary); outline-offset: 3px; }
</style>
