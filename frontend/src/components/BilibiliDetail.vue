<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { bilibiliApi } from "../api/feedback";
import type { BilibiliContent } from "../api/types";
import { calendarDate } from "../format";
import { detailMediaGroups } from "../media-detail";
import { defaultMediaIds, mediaSelectionSession, type SavedMediaSelection } from "../media-selection";
import BilibiliMediaChoices from "./BilibiliMediaChoices.vue";

/** Integration: <BilibiliDetail v-model="open" :content-id="id" @saved="..." />.
 * Loads its own detail, saves into mediaSelectionSession, never opens/submits a preview.
 * DownloadPreview reads the same store automatically. Closing discards this drawer's draft.
 */
const props = defineProps<{ modelValue: boolean; contentId: string | null }>();
const emit = defineEmits<{ "update:modelValue": [open: boolean]; saved: [selection: SavedMediaSelection]; close: [] }>();
const content = ref<BilibiliContent | null>(null);
const selectedIds = ref<string[]>([]), descriptionOpen = ref(false), busy = ref(false), error = ref("");
const changedVersion = ref(false), missingChoices = ref(false);
let generation = 0;
const groups = computed(() => content.value ? detailMediaGroups(content.value) : []);
const media = computed(() => groups.value.flatMap((group) => group.media));
const allIds = computed(() => defaultMediaIds(media.value));
const identifierLabel = computed(() => ({ BVID: "BV 号", SEASON_ID: "季标识", EPISODE_ID: "剧集标识", MEDIA_ID: "作品标识" }[content.value?.externalIdType ?? "BVID"]));
const downloadLabels: Record<string, string> = { NOT_REQUESTED: "未下载", QUEUED: "等待下载", DOWNLOADING: "正在下载", RUNNING: "正在下载", PARTIAL: "部分内容已下载", VERIFIED: "已下载", NEEDS_ACTION: "下载需要处理", UNKNOWN: "下载状态待确认" };
const archiveLabels: Record<string, string> = { NOT_REQUESTED: "尚未归档", QUEUED: "等待归档", UPLOADING: "正在上传", VERIFYING: "正在核验归档", VERIFIED: "已归档", NEEDS_ACTION: "归档需要处理", UNKNOWN: "归档状态待确认" };
async function load() {
  const g = ++generation;
  content.value = null;
  selectedIds.value = [];
  descriptionOpen.value = false;
  error.value = "";
  if (!props.contentId) return;
  busy.value = true;
  try {
    const next = await bilibiliApi.content(props.contentId);
    if (g !== generation) return;
    content.value = next;
    const previous = mediaSelectionSession.get(next.id);
    const draft = mediaSelectionSession.draft(next.id, next.revision, media.value);
    const allowed = new Set(allIds.value);
    selectedIds.value = draft.mediaIds.filter((id) => allowed.has(id));
    changedVersion.value = !!previous && previous.contentVersion !== next.revision;
    missingChoices.value = draft.mediaIds.some((id) => !allowed.has(id));
  } catch (cause) {
    if (g === generation) error.value = cause instanceof Error ? cause.message : "详情获取失败";
  } finally { if (g === generation) busy.value = false; }
}
function save() {
  if (!content.value || busy.value) return;
  const saved = mediaSelectionSession.save({ contentId: content.value.id,
    contentVersion: content.value.revision, mediaIds: [...selectedIds.value] });
  emit("saved", saved);
  emit("update:modelValue", false);
}
watch(() => [props.modelValue, props.contentId] as const, ([open]) => {
  if (open) void load(); else { generation++; busy.value = false; }
}, { immediate: true });
onBeforeUnmount(() => generation++);
</script>

<template>
  <el-drawer :model-value="modelValue" title="内容详情" size="min(560px, 90vw)" class="bilibili-detail"
    @update:model-value="emit('update:modelValue', $event)" @closed="emit('close')">
    <div v-loading="busy" class="detail-body">
      <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
      <el-button v-if="error" class="retry-detail" @click="load">重试加载详情</el-button>
      <template v-if="content">
        <img v-if="content.coverUrl" class="content-cover" :src="content.coverUrl" :alt="content.title + '封面'" />
        <div v-else class="missing-cover">暂无封面</div>
        <h2 class="content-title">{{ content.title }}</h2>
        <p class="content-byline">UP 主：{{ content.author?.name || '未知' }}<span>上传日期：{{ calendarDate(content.publishedAt) }}</span></p>
        <section class="description-section" aria-label="简介">
          <p :class="{ collapsed: !descriptionOpen }">{{ content.description || '暂无简介' }}</p>
          <button v-if="content.description" type="button" class="text-control" :aria-expanded="descriptionOpen"
            @click="descriptionOpen = !descriptionOpen">{{ descriptionOpen ? '收起简介' : '展开简介' }}</button>
        </section>
        <dl class="content-facts">
          <dt>{{ identifierLabel }}</dt><dd>{{ content.externalId }}</dd>
          <dt>下载状态</dt><dd>{{ downloadLabels[content.downloadSummary.state] || '下载状态待确认' }}</dd>
          <dt>归档状态</dt><dd>{{ archiveLabels[content.archiveSummary.state] || '归档状态待确认' }}</dd>
          <dt>来源</dt><dd><ul v-if="content.sources.length"><li v-for="source in content.sources" :key="source.id">{{ source.displayLabel }}</li></ul><span v-else>暂无有效来源</span></dd>
        </dl>
        <a class="text-control" :href="'/downloads?platform=BILIBILI&contentId=' + encodeURIComponent(content.id)">查看下载记录</a>
        <el-alert v-if="content.availability !== 'AVAILABLE'" :title="content.availabilityReason || '可下载状态待确认'" type="warning" :closable="false" />
        <p v-for="warning in content.warnings" :key="warning" class="detail-warning">{{ warning }}</p>
        <div class="media-heading"><h3>选择下载内容</h3><div>
          <el-button link @click="selectedIds = [...allIds]" :disabled="!allIds.length">全选</el-button>
          <el-button link @click="selectedIds = []" :disabled="!selectedIds.length">全不选</el-button>
        </div></div>
        <p v-if="changedVersion || missingChoices" class="detail-warning" role="status">内容信息已更新，请核对当前选项后保存。新出现的内容未自动加入；失效选项已移出草稿。</p>
        <BilibiliMediaChoices :key="content.id" :groups="groups" v-model:selected-ids="selectedIds" />
        <p v-if="!groups.length" class="detail-warning">暂无媒体信息，刷新来源后重试。</p>
      </template>
    </div>
    <template #footer>
      <div class="detail-footer">
        <p role="status">{{ selectedIds.length ? `已选 ${selectedIds.length} 项；保存后供下载清单使用。` : '未选择内容。保存后该内容不能提交下载，需重新选择。' }}</p>
        <div class="detail-buttons"><el-button @click="emit('update:modelValue', false)">关闭</el-button>
          <el-button type="primary" :disabled="busy || !content" @click="save">保存选定内容</el-button></div>
      </div>
    </template>
  </el-drawer>
</template>

<style scoped>
.detail-body { min-height: 160px; }
.content-cover, .missing-cover { width: 100%; aspect-ratio: 16 / 9; object-fit: cover; border-radius: 8px; }
.missing-cover { display: grid; place-items: center; background: var(--el-fill-color-light); color: var(--muted); }
.content-title { margin: 18px 0 10px; overflow-wrap: anywhere; }
.content-byline { display: flex; flex-wrap: wrap; column-gap: 18px; row-gap: 4px; color: var(--muted); }
.description-section { padding-bottom: 18px; border-bottom: 1px solid var(--line); }
.description-section p { white-space: pre-wrap; overflow-wrap: anywhere; margin-bottom: 6px; }
.description-section p.collapsed { display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.text-control { border: 0; padding: 4px 0; color: var(--primary); background: none; }
.content-facts { display: grid; grid-template-columns: 76px minmax(0, 1fr); gap: 12px 18px; padding: 18px 0; margin: 0; }
.content-facts dt { color: var(--muted); }
.content-facts dd { margin: 0; overflow-wrap: anywhere; }
.content-facts ul { list-style: none; padding: 0; margin: 0; }
.media-heading { display: flex; align-items: baseline; justify-content: space-between; gap: 16px; margin: 22px 0 12px; }
.media-heading h3 { margin: 0; }
.detail-warning { color: var(--el-color-warning); margin: 10px 0; }
.detail-footer { display: grid; gap: 10px; text-align: left; }
.detail-footer p { margin: 0; color: var(--muted); font-size: 13px; }
.detail-buttons { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 12px; }
.detail-buttons :deep(.el-button + .el-button) { margin-left: 0; }
.retry-detail { margin-top: 12px; }
.text-control:focus-visible { outline: 2px solid var(--primary); outline-offset: 3px; }
</style>
