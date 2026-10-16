<script setup lang="ts" generic="T extends { id: string }">
import { computed, onBeforeUnmount, ref } from "vue";
import { resizeColumn, toggleColumn, type ColumnWidth } from "../download-column-widths";
const props = defineProps<{
  rows: T[];
  columns: { key: string; label: string; min: number; width: number; sortable?: boolean }[];
  sortKey?: string;
  sortDirection?: "asc" | "desc";
  loading?: boolean;
}>();
const emit = defineEmits<{ sort: [key: string] }>();
const table = ref<HTMLTableElement | null>(null);
const sizes = ref<Record<string, ColumnWidth>>(Object.fromEntries(props.columns.map(c => [c.key, { width: c.width, min: c.min, mode: "manual" }])));
const dragging = ref(false);
const total = computed(() => Object.values(sizes.value).reduce((sum, c) => sum + c.width, 0));
let stopDrag: (() => void) | undefined;
function measure(key: string) {
  // Only loaded DOM rows; never fetch additional history for a presentation setting.
  const cells = [...(table.value?.querySelectorAll<HTMLElement>("[data-measure]") ?? [])].filter(el => el.dataset.measure === key);
  return Math.max(sizes.value[key]!.min, ...cells.map(el => Math.ceil(el.scrollWidth) + 40));
}
function toggle(key: string) { sizes.value[key] = toggleColumn(sizes.value[key]!, measure(key)); }
function fit(key: string) { sizes.value[key] = { ...sizes.value[key]!, width: measure(key), mode: "fit" }; }
function minimum(key: string) { sizes.value[key] = { ...sizes.value[key]!, width: sizes.value[key]!.min, mode: "min" }; }
function adjust(key: string, delta: number) { sizes.value[key] = resizeColumn(sizes.value[key]!, sizes.value[key]!.width + delta); }
function begin(event: PointerEvent, key: string) {
  if (event.button !== 0) return;
  stopDrag?.();
  const element = event.currentTarget as HTMLElement, x = event.clientX, width = sizes.value[key]!.width;
  let moved = false;
  element.setPointerCapture(event.pointerId);
  const move = (e: PointerEvent) => {
    if (e.pointerId !== event.pointerId || Math.abs(e.clientX - x) < 2 && !moved) return;
    moved = true; dragging.value = true;
    sizes.value[key] = resizeColumn(sizes.value[key]!, width + e.clientX - x);
  };
  const end = () => {
    dragging.value = false;
    element.removeEventListener("pointermove", move);
    element.removeEventListener("pointerup", end);
    element.removeEventListener("pointercancel", end);
    element.removeEventListener("lostpointercapture", end);
    if (element.hasPointerCapture(event.pointerId)) element.releasePointerCapture(event.pointerId);
    stopDrag = undefined;
  };
  element.addEventListener("pointermove", move);
  element.addEventListener("pointerup", end);
  element.addEventListener("pointercancel", end);
  element.addEventListener("lostpointercapture", end);
  stopDrag = end;
}
function keydown(event: KeyboardEvent, key: string) {
  if (!["ArrowLeft", "ArrowRight", "Home", "End", "Enter", " "].includes(event.key)) return;
  event.preventDefault(); event.stopPropagation();
  if (event.key === "Home") minimum(key);
  else if (event.key === "End") fit(key);
  else if (event.key === "Enter" || event.key === " ") toggle(key);
  else adjust(key, (event.key === "ArrowLeft" ? -1 : 1) * (event.shiftKey ? 40 : 10));
}
onBeforeUnmount(() => stopDrag?.());
</script>
<template>
  <div class="download-table">
    <div class="width-toolbar">
      <span id="download-width-help">拖动列头分隔线调整；双击切换适宽 / 最小宽度。聚焦后可用左右键、Enter。</span>
      <el-popover placement="bottom-end" :width="430" trigger="click">
        <template #reference><el-button size="small">调整列宽</el-button></template>
        <div v-for="column in columns" :key="column.key" class="width-option">
          <span>{{ column.label }}</span>
          <button type="button" :aria-label="column.label + '缩窄'" @click="adjust(column.key, -20)">−</button>
          <button type="button" :aria-label="column.label + '加宽'" @click="adjust(column.key, 20)">＋</button>
          <button type="button" @click="fit(column.key)">适应内容</button>
          <button type="button" @click="minimum(column.key)">最小宽度</button>
        </div>
      </el-popover>
    </div>
    <div class="table-scroll" tabindex="0" aria-label="下载项目表格，可横向滚动" :class="{ resizing: dragging }" :aria-busy="loading">
      <table ref="table" :style="{ width: total + 'px' }">
        <caption class="sr-only">下载项目；排序仅作用于已加载的项目</caption>
        <colgroup><col v-for="column in columns" :key="column.key" :style="{ width: sizes[column.key]!.width + 'px' }" /></colgroup>
        <thead><tr><th v-for="column in columns" :key="column.key" scope="col"
          :aria-sort="column.sortable ? (sortKey === column.key ? (sortDirection === 'asc' ? 'ascending' : 'descending') : 'none') : undefined">
          <button v-if="column.sortable" type="button" class="sort-heading" :data-measure="column.key" @click="emit('sort', column.key)">
            {{ column.label }} <span aria-hidden="true">{{ sortKey === column.key ? (sortDirection === 'asc' ? '↑' : '↓') : '↕' }}</span>
          </button>
          <span v-else :data-measure="column.key" class="cell-content">{{ column.label }}</span>
          <span role="separator" tabindex="0" aria-orientation="vertical" :aria-label="'调整' + column.label + '列宽'" aria-describedby="download-width-help"
            :aria-valuemin="column.min" :aria-valuenow="sizes[column.key]!.width" :aria-valuetext="sizes[column.key]!.width + '像素'"
            class="column-resizer" @pointerdown.stop.prevent="begin($event, column.key)" @click.stop @dblclick.stop.prevent="toggle(column.key)" @keydown="keydown($event, column.key)" />
        </th></tr></thead>
        <tbody><tr v-for="row in rows" :key="row.id" :data-project-id="row.id">
          <td v-for="column in columns" :key="column.key"><div class="cell-clip"><div class="cell-content" :data-measure="column.key"><slot :name="column.key" :row="row" /></div></div></td>
        </tr><tr v-if="!rows.length"><td :colspan="columns.length"><div class="table-empty"><slot name="empty">暂无下载项目</slot></div></td></tr></tbody>
      </table>
    </div>
  </div>
</template>
<style scoped>
.width-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 10px 20px; color: var(--muted); font-size: 13px; }
.width-option { display: flex; gap: 6px; align-items: center; margin: 8px 0; }
.width-option span { width: 70px; }
.width-option button { padding: 5px 8px; background: var(--el-fill-color-light); border: 1px solid var(--line); border-radius: 4px; cursor: pointer; }
.table-scroll { overflow-x: auto; max-width: 100%; }
table { border-collapse: collapse; table-layout: fixed; text-align: left; font-size: 14px; }
th { position: relative; height: 46px; background: var(--el-fill-color-light); font-weight: 500; }
th, td { padding: 12px 20px; border-bottom: 1px solid var(--line); box-sizing: border-box; }
td { height: 80px; vertical-align: middle; }
tbody tr:hover { background: var(--el-fill-color-lighter); }
.cell-clip { overflow: hidden; }
.cell-content { display: inline-block; width: max-content; min-width: 0; white-space: nowrap; }
.sort-heading { border: none; background: none; color: inherit; font: inherit; padding: 0; cursor: pointer; white-space: nowrap; }
.column-resizer { position: absolute; top: 0; right: 0; width: 12px; height: 100%; cursor: col-resize; touch-action: none; user-select: none; z-index: 1; }
.column-resizer::after { content: ''; position: absolute; right: 3px; top: 10px; bottom: 10px; width: 1px; background: var(--el-border-color-darker); }
.column-resizer:hover::after, .column-resizer:focus-visible::after { width: 3px; background: var(--primary); }
.resizing { user-select: none; cursor: col-resize; }
.table-empty { padding: 40px 0; color: var(--muted); white-space: normal; }
button:focus-visible, .column-resizer:focus-visible, .table-scroll:focus-visible { outline: 2px solid var(--primary); outline-offset: -2px; }
.sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }
</style>
