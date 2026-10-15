<script setup lang="ts">
import { ref } from "vue";
import { ChevronDown } from "lucide-vue-next";
import type { DetailMediaGroup } from "../media-detail";
import { isSelectableMedia } from "../media-selection";
import { duration, formatBytes } from "../format";

const props = defineProps<{ groups: DetailMediaGroup[]; selectedIds: string[] }>();
const emit = defineEmits<{ "update:selectedIds": [ids: string[]] }>();
const expanded = ref<string[]>([]);
function toggleGroup(id: string) {
  expanded.value = expanded.value.includes(id)
    ? expanded.value.filter((value) => value !== id) : [...expanded.value, id];
}
function toggleMedia(id: string, checked: boolean) {
  emit("update:selectedIds", checked
    ? [...new Set([...props.selectedIds, id])]
    : props.selectedIds.filter((value) => value !== id));
}
function visibleMedia(group: DetailMediaGroup) {
  return group.media.filter((item) => item.kind !== "SUBTITLE" || item.subtitleKind === "HUMAN");
}
function selectedCount(group: DetailMediaGroup) {
  return visibleMedia(group).filter((item) => props.selectedIds.includes(item.id)).length;
}
function outputHint(group: DetailMediaGroup) {
  const kinds = new Set(group.media.filter((item) => props.selectedIds.includes(item.id)).map((item) => item.kind));
  if (kinds.has("VIDEO") && kinds.has("AUDIO")) return "视频与音频合并保存";
  if (kinds.has("VIDEO")) return "仅保存视频，不含音频";
  if (kinds.has("AUDIO")) return "仅保存音频";
  if (kinds.size) return "仅保存选中的字幕或弹幕";
  return "此单元未选择内容";
}
</script>

<template>
  <div class="media-groups" @click.stop>
    <section v-for="group in groups" :key="group.id" class="media-group">
      <button v-if="group.collapsible" class="group-toggle" type="button"
        :aria-expanded="expanded.includes(group.id)" :aria-controls="'media-group-' + group.id"
        @click="toggleGroup(group.id)">
        <ChevronDown :size="18" aria-hidden="true" :class="{ folded: !expanded.includes(group.id) }" />
        <span>{{ group.label }}</span>
        <small>已选 {{ selectedCount(group) }} 项</small>
      </button>
      <div v-show="!group.collapsible || expanded.includes(group.id)" :id="'media-group-' + group.id"
        class="group-body">
        <div class="media-options">
          <label v-for="item in visibleMedia(group)" :key="item.id" class="media-option"
            :class="{ unavailable: !isSelectableMedia(item) }">
            <input type="checkbox" :checked="selectedIds.includes(item.id)" :disabled="!isSelectableMedia(item)"
              :aria-label="group.label + '：' + item.label"
              @change="toggleMedia(item.id, ($event.target as HTMLInputElement).checked)" />
            <span class="media-label">{{ item.label }}
              <small v-if="!isSelectableMedia(item)">{{ item.availabilityReason || '可下载状态待确认' }}</small>
            </span>
            <span class="media-fact">{{ item.kind === 'VIDEO' || item.kind === 'AUDIO'
              ? duration(item.durationSeconds) : formatBytes(item.sizeBytes) }}</span>
          </label>
        </div>
        <p v-if="!visibleMedia(group).length" class="media-note">暂无可选媒体，等待信息获取。</p>
        <p v-for="warning in group.warnings" :key="warning" class="media-note">{{ warning }}</p>
        <p class="output-hint">{{ outputHint(group) }}</p>
      </div>
    </section>
  </div>
</template>

<style scoped>
.media-groups { display: grid; gap: 12px; }
.media-group { border: 1px solid var(--line); border-radius: 8px; overflow: hidden; }
.group-toggle { width: 100%; border: 0; background: var(--el-fill-color-light, #f5f7f6); display: flex;
  align-items: center; gap: 10px; padding: 14px 16px; text-align: left; }
.group-toggle > span { flex: 1; min-width: 0; overflow-wrap: anywhere; font-weight: 600; }
.group-toggle small { color: var(--muted); white-space: nowrap; font-size: 13px; }
.group-toggle svg { transition: transform .15s; }
.group-toggle svg.folded { transform: rotate(-90deg); }
.group-body { padding: 8px 16px 12px; }
.media-options { display: grid; }
.media-option { min-height: 44px; display: flex; align-items: center; gap: 10px; cursor: pointer; padding: 6px 0; }
.media-option input { width: 16px; height: 16px; flex-shrink: 0; accent-color: var(--primary); margin: 0; }
.media-label { flex: 1; min-width: 0; overflow-wrap: anywhere; }
.media-label small { display: block; color: var(--muted); font-size: 13px; }
.media-fact { color: var(--muted); white-space: nowrap; font-variant-numeric: tabular-nums; }
.unavailable { cursor: default; }
.media-note, .output-hint { color: var(--muted); font-size: 13px; margin: 8px 0 0; }
.output-hint { border-top: 1px solid var(--line); padding-top: 10px; }
.group-toggle:focus-visible, input:focus-visible { outline: 2px solid var(--primary); outline-offset: -3px; }
@media (prefers-reduced-motion: reduce) { .group-toggle svg { transition: none; } }
</style>
