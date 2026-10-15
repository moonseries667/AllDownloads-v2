<script setup lang="ts">
import { computed, ref, watch, onBeforeUnmount } from "vue";
import { ArrowDownAZ, ArrowUpAZ, Download, FolderOpen, RefreshCw } from "lucide-vue-next";
import { api, notifyError, notifySuccess } from "../api/client";
import { downloadApi, selectionWithSavedMedia } from "../api/feedback";
import type { DownloadTarget, Platform, Selection, Preview } from "../api/types";
import { mediaSelectionSession } from "../media-selection";
import { formatBytes } from "../format";
import { hasReliableSizes, previewAction, previewCounts, previewFact } from "../preview-presentation";

const props = defineProps<{ modelValue: boolean; platform: Platform; selection: Selection }>();
const emit = defineEmits<{ "update:modelValue": [value: boolean]; submitted: [] }>();
const preview = ref<Preview | null>(null), busy = ref(false), submitting = ref(false), error = ref("");
const invalid = ref(false), defaultRoot = ref(""), target = ref<DownloadTarget | null>(null);
const editingTarget = ref(false), targetPath = ref(""), targetError = ref(""), validating = ref(false);
const titleAscending = ref(true);
const frozenSelection = ref<Selection | null>(null);
const sortedItems = computed(() => [...(preview.value?.items ?? [])].sort((a, b) =>
  (titleAscending.value ? 1 : -1) * String(a.title).localeCompare(String(b.title), "zh-CN") ||
  String(a.id ?? a.partId ?? a.nodeId).localeCompare(String(b.id ?? b.partId ?? b.nodeId))));
const counts = computed(() => previewCounts(preview.value?.items ?? []));
const showSize = computed(() => hasReliableSizes(preview.value?.items ?? []));
const effectiveRoot = computed(() => target.value?.path || (!invalid.value && preview.value?.target?.path) || defaultRoot.value);
const noArchive = computed(() => target.value?.archivePolicy === "NONE" || (!invalid.value && preview.value?.archivePolicy === "NONE"));
const savedSignature = computed(() => {
  if (props.platform !== "BILIBILI") return "";
  const ids = frozenSelection.value?.ids ?? props.selection.ids;
  return JSON.stringify(ids ? mediaSelectionSession.snapshotsFor(ids) : mediaSelectionSession.entries.value);
});
let generation = 0, targetGeneration = 0;
let requestSavedSignature = "";
let timer: ReturnType<typeof setTimeout> | undefined, targetTimer: ReturnType<typeof setTimeout> | undefined;
function invalidate(message: string) {
  generation++;
  clearTimeout(timer);
  busy.value = false;
  invalid.value = true;
  error.value = message;
}
async function prepare() {
  if (submitting.value || validating.value || editingTarget.value) return;
  const g = ++generation;
  busy.value = true;
  error.value = "";
  invalid.value = true;
  clearTimeout(timer);
  requestSavedSignature = savedSignature.value;
  try {
    const settings = await api.get("/settings");
    if (g !== generation) return;
    defaultRoot.value = settings.downloadRoot;
    const selection = props.platform === "BILIBILI"
      ? await selectionWithSavedMedia(frozenSelection.value ?? props.selection)
      : JSON.parse(JSON.stringify(props.selection));
    if (g !== generation) return;
    frozenSelection.value = selection;
    requestSavedSignature = savedSignature.value;
    const receipt = await downloadApi.preview(props.platform, selection,
      target.value ? { id: target.value.id, revision: target.value.revision } : undefined);
    if (g !== generation) return;
    const read = async () => {
      try {
        const data = await downloadApi.readPreview(receipt.previewId);
        if (g !== generation) return;
        preview.value = data;
        if (data.status === "PREPARING") timer = setTimeout(read, 2000);
        else {
          busy.value = false;
          invalid.value = data.status !== "READY";
          if (invalid.value) error.value = data.status === "EXPIRED" ? "预览已过期，请刷新。" : "此清单需要处理，请检查原因后刷新。";
        }
      } catch (cause) {
        if (g === generation) { error.value = (cause as Error).message; busy.value = false; invalid.value = true; }
      }
    };
    await read();
  } catch (cause) {
    if (g === generation) { error.value = (cause as Error).message; busy.value = false; invalid.value = true; }
  }
}
function editTarget() {
  targetPath.value = effectiveRoot.value;
  targetError.value = "";
  editingTarget.value = true;
}
function cancelTarget() {
  targetGeneration++;
  clearTimeout(targetTimer);
  validating.value = false;
  editingTarget.value = false;
  targetError.value = "";
}
async function validateTarget() {
  const path = targetPath.value.trim();
  if (!path) { targetError.value = "请输入产品运行主机上的本机目录。"; notifyError(new Error(targetError.value)); return; }
  const g = ++targetGeneration;
  validating.value = true;
  targetError.value = "";
  try {
    const receipt = await downloadApi.validateTarget(path);
    const read = async () => {
      try {
        const result = await downloadApi.target(receipt.targetId);
        if (g !== targetGeneration) return;
        if (result.status === "VALIDATING") { targetTimer = setTimeout(read, 2000); return; }
        validating.value = false;
        if (result.status !== "VALID") { targetError.value = String(result.error?.message || "目录校验未通过，请选择其他目录。"); notifyError(new Error(targetError.value)); return; }
        if (result.archivePolicy !== "NONE") { targetError.value = "此目标没有确认本批不归档，请重新校验目录。"; notifyError(new Error(targetError.value)); return; }
        target.value = result;
        editingTarget.value = false;
        invalidate("本批下载目录已更改，请点击“刷新”确认新的清单。仅影响本批次，不进入归档。");
      } catch (cause) { if (g === targetGeneration) { targetError.value = (cause as Error).message; validating.value = false; } }
    };
    if (g === targetGeneration) await read();
  } catch (cause) { if (g === targetGeneration) { targetError.value = (cause as Error).message; validating.value = false; } }
}
function restoreDefault() {
  target.value = null;
  cancelTarget();
  invalidate("已恢复全局默认目录，请点击“刷新”确认清单和归档规则。");
}
async function submit() {
  if (!preview.value || invalid.value || busy.value || validating.value || editingTarget.value || submitting.value) return;
  if (Date.parse(preview.value.expiresAt) <= Date.now()) { invalidate("预览已过期，请刷新后提交。"); return; }
  submitting.value = true;
  error.value = "";
  try {
    await downloadApi.submit(preview.value.id);
    notifySuccess("下载订单已排队。可在下载管理中查看；尚未下载完成。");
    target.value = null;
    frozenSelection.value = null;
    emit("submitted");
    emit("update:modelValue", false);
  } catch (cause) { error.value = (cause as Error).message; invalid.value = true; }
  finally { submitting.value = false; }
}
watch(() => [props.modelValue, props.platform, JSON.stringify(props.selection)] as const, ([open, platform, selection], previous) => {
  const selectionChanged = previous && (selection !== previous[2] || platform !== previous[1]);
  if (selectionChanged) frozenSelection.value = null;
  if (open && !previous?.[0]) void prepare();
  else if (open && selectionChanged) invalidate("下载选择已更改，请点击“刷新”确认清单。");
  else if (!open) { generation++; clearTimeout(timer); busy.value = false; cancelTarget(); }
}, { immediate: true });
watch(mediaSelectionSession.revision, () => {
  if (savedSignature.value !== requestSavedSignature && props.modelValue && (preview.value || busy.value))
    invalidate("媒体选择已保存，请点击“刷新”确认清单。");
});
onBeforeUnmount(() => { generation++; targetGeneration++; clearTimeout(timer); clearTimeout(targetTimer); });
</script>

<template>
  <el-dialog :model-value="modelValue" title="确认下载清单" width="min(880px, 92vw)" class="download-preview"
    @update:model-value="emit('update:modelValue', $event)" :close-on-click-modal="false" :close-on-press-escape="!submitting" :show-close="!submitting">
    <p class="dialog-intro">仅处理本次清单中的内容。之后新增的内容不会自动加入。</p>
    <div class="download-target"><FolderOpen :size="20" aria-hidden="true" /><div class="target-description">
      <span>下载到...</span><strong>{{ effectiveRoot || '读取中…' }}</strong></div>
      <el-button @click="editTarget" :disabled="submitting || validating">修改目录</el-button></div>
    <p v-if="noArchive" class="target-note">仅本批下载到指定目录，不进入归档；全局默认目录保持不变。</p>
    <p v-else class="target-note">使用全局默认目录；下载与归档状态分别记录。</p>
    <section v-if="editingTarget" class="target-editor" aria-label="修改本批下载目录">
      <label for="batch-target-path">产品运行主机的本机目录</label>
      <el-input id="batch-target-path" v-model="targetPath" :disabled="validating" placeholder="例如 D:\Downloads\本批内容" @keydown.enter.prevent="validateTarget" />
      <p>由服务端校验目录；只用于本批下载，不归档。</p>
      <p v-if="targetError" role="alert" class="target-error">{{ targetError }}</p>
      <p v-if="validating" role="status">正在由运行主机检查目录，请稍候…</p>
      <div class="preview-buttons"><el-button @click="cancelTarget">取消修改</el-button>
        <el-button v-if="target" @click="restoreDefault" :disabled="validating">恢复默认目录</el-button>
        <el-button type="primary" :loading="validating" @click="validateTarget">校验并使用此目录</el-button></div>
    </section>
    <el-button v-else-if="target" link @click="restoreDefault" :disabled="submitting">恢复默认目录</el-button>
    <el-alert v-if="error" :title="error" :type="invalid && preview ? 'warning' : 'error'" :closable="false" show-icon class="preview-alert" />
    <div v-loading="busy" class="preview-body" :aria-busy="busy">
      <template v-if="preview">
        <div class="fact-counts"><span v-for="count in counts" :key="count.label"><b>{{ count.count }}</b>{{ count.label }}</span></div>
        <el-table :data="sortedItems" max-height="330" :class="{ 'stale-preview': invalid }">
          <el-table-column prop="title" min-width="260">
            <template #header><button type="button" class="title-sort" @click="titleAscending = !titleAscending"
              :aria-label="'标题，当前' + (titleAscending ? '正序' : '逆序') + '，点击切换'">标题
              <component :is="titleAscending ? ArrowDownAZ : ArrowUpAZ" :size="16" aria-hidden="true" /></button></template>
            <template #default="{ row }"><strong class="preview-title">{{ row.title }}</strong></template>
          </el-table-column>
          <el-table-column v-if="showSize" label="大小" width="110"><template #default="{ row }">{{ formatBytes(row.sizeBytes) }}</template></el-table-column>
          <el-table-column label="状态" width="140"><template #default="{ row }">{{ previewFact(row) }}</template></el-table-column>
          <el-table-column label="处理方式" min-width="220"><template #default="{ row }"><span :class="{ 'target-error': row.decision === 'BLOCKED' }">{{ previewAction(row, preview?.archivePolicy) }}</span><p v-if="row.reason" class="item-reason">{{ row.reason }}</p></template></el-table-column>
        </el-table>
        <p v-for="warning in preview.warnings.filter((value) => !value.startsWith('质量策略'))" :key="warning" class="preview-warning">{{ warning }}</p>
      </template>
    </div>
    <template #footer><div class="preview-buttons">
      <el-button @click="emit('update:modelValue', false)" :disabled="submitting">返回选择</el-button>
      <el-button :icon="RefreshCw" @click="prepare" :loading="busy" :disabled="submitting || validating || editingTarget">刷新</el-button>
      <el-button type="primary" :icon="Download" :loading="submitting"
        :disabled="busy || invalid || validating || editingTarget || preview?.status !== 'READY' || !preview?.summary.download" @click="submit">提交下载</el-button>
    </div></template>
  </el-dialog>
</template>

<style scoped>
:global(.el-dialog.download-preview) { margin: 24px auto; max-height: calc(100dvh - 48px); display: flex; flex-direction: column; }
:global(.download-preview .el-dialog__body) { min-height: 0; overflow: auto; }
:global(.download-preview .el-dialog__header), :global(.download-preview .el-dialog__footer) { flex-shrink: 0; }
.download-target { display: flex; gap: 14px; align-items: center; padding: 16px; border-radius: 8px; background: var(--el-fill-color-light); }
.target-description { flex: 1; min-width: 0; }
.target-description span { display: block; color: var(--muted); font-size: 13px; }
.target-description strong { display: block; overflow-wrap: anywhere; }
.target-note, .preview-warning { color: var(--muted); margin: 10px 0 16px; font-size: 13px; }
.target-editor { padding: 16px; margin-bottom: 16px; border: 1px solid var(--line); border-radius: 8px; }
.target-editor label { display: block; margin-bottom: 8px; font-weight: 600; }
.target-editor p { margin: 10px 0; font-size: 13px; }
.target-error { color: var(--el-color-danger); }
.preview-alert { margin: 14px 0; }
.preview-body { min-height: 120px; }
.fact-counts { display: flex; flex-wrap: wrap; align-items: baseline; gap: 12px 24px; padding: 16px 0; }
.fact-counts span { display: inline-flex; align-items: baseline; gap: 6px; }
.fact-counts b { font-size: 20px; font-variant-numeric: tabular-nums; }
.preview-title { overflow-wrap: anywhere; }
.item-reason { margin: 4px 0 0; font-size: 13px; color: var(--muted); }
.title-sort { display: inline-flex; align-items: center; gap: 8px; border: 0; background: none; padding: 4px 0; font-weight: 600; }
.title-sort:focus-visible { outline: 2px solid var(--primary); outline-offset: 2px; }
.stale-preview { opacity: .7; }
.preview-buttons { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 12px; }
.preview-buttons :deep(.el-button + .el-button) { margin-left: 0; }
</style>
