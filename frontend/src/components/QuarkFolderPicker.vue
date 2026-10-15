<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from "vue";
import { ChevronRight, Folder } from "lucide-vue-next";
import DirectoryBrowser from "./DirectoryBrowser.vue";
import { api, notifyError } from "../api/client";
import { folderPath, loadQuarkFolders, selectableFolder, type QuarkFolderPage, type QuarkTarget } from "../api/quark-folders";

const props = defineProps<{ modelValue: QuarkTarget | null; accountId: string; label: string; disabled?: boolean }>();
const emit = defineEmits<{ "update:modelValue": [value: QuarkTarget | null] }>();
const visible = ref(false);
const page = ref<QuarkFolderPage | null>(null);
const loading = ref(false);
const error = ref("");
const requestedId = ref<string>();
let sequence = 0;
const currentPath = computed(() => page.value ? folderPath(page.value) : "");
const currentTarget = computed(() => !loading.value && !error.value && page.value ? selectableFolder(page.value) : null);
const ancestors = computed(() => page.value?.breadcrumbs.filter((node) => node.parentId !== null && node.id !== page.value?.parent?.id) ?? []);

async function load(parentId?: string, append = false) {
  const request = ++sequence;
  const previous = page.value;
  requestedId.value = parentId;
  loading.value = true;
  error.value = "";
  if (!append) page.value = null;
  try {
    const result = await loadQuarkFolders({ request: (_method, path) => api.get(path) }, props.accountId, parentId, append ? previous?.nextCursor ?? undefined : undefined);
    if (request !== sequence || !visible.value) return;
    page.value = append && previous?.parent?.id === result.parent?.id && previous?.parent?.revision === result.parent?.revision
      ? { ...result, items: [...new Map([...previous!.items, ...result.items].map((node) => [node.id, node])).values()] }
      : result;
  } catch (cause) {
    if (request !== sequence || !visible.value) return;
    error.value = cause instanceof Error ? cause.message : "目录读取失败，请重试";
    notifyError(cause);
  } finally {
    if (request === sequence) loading.value = false;
  }
}
function open() {
  visible.value = true;
  void load(props.modelValue?.id);
}
function select() {
  if (!currentTarget.value) return;
  emit("update:modelValue", currentTarget.value);
  visible.value = false;
}
function close() { sequence++; loading.value = false; }
onBeforeUnmount(close);
</script>

<template>
  <div class="quark-target-field">
    <el-button class="target-trigger" :aria-label="label" :disabled="disabled" @click="open">
      <Folder :size="16" aria-hidden="true" />
      <span>{{ modelValue ? (modelValue.path || "我的云盘") : "选择夸克文件夹" }}</span>
      <ChevronRight :size="16" aria-hidden="true" />
    </el-button>
    <el-dialog v-model="visible" title="选择夸克文件夹" class="quark-folder-dialog" width="min(620px, calc(100vw - 48px))" append-to-body destroy-on-close @closed="close">
      <DirectoryBrowser root-label="我的云盘" path-label="夸克文件夹路径" :current-name="page?.parent?.parentId !== null && page?.parent ? page.parent.name : null" :parent-id="page?.parent?.parentId ?? null" :ancestors="ancestors" :items="page?.items.map(folder => ({ id: folder.id, name: folder.name, disabled: folder.availability !== 'AVAILABLE', reason: folder.availabilityReason })) ?? []" :loading="loading" :problem="error || (page?.status === 'NEEDS_ACTION' ? page.parent?.availabilityReason || '目录暂不可用，请检查账号或返回上级重新选择。' : '')" :pending="page?.status === 'LOADING'" :next-cursor="page?.nextCursor" :selectable="!!currentTarget" :selection-reason="page?.parent?.availabilityReason || '当前文件夹不可作为转存目标。'" @navigate="load($event)" @refresh="load(requestedId)" @more="load(requestedId, true)"><template #current>{{ page ? (currentPath || '我的云盘') : '尚未读取' }}</template></DirectoryBrowser>
      <template #footer><div class="folder-footer"><el-button @click="visible = false">取消</el-button><el-button type="primary" :disabled="!currentTarget" @click="select">使用此文件夹</el-button></div></template>
    </el-dialog>
  </div>
</template>

<style scoped>
:global(.el-dialog.quark-folder-dialog) { margin: 24px auto; max-height: calc(100dvh - 48px); display: flex; flex-direction: column; }
:global(.quark-folder-dialog .el-dialog__body) { min-height: 0; overflow-y: auto; }
:global(.quark-folder-dialog .el-dialog__header), :global(.quark-folder-dialog .el-dialog__footer) { flex-shrink: 0; }
.quark-target-field, .target-trigger { width: 100%; min-width: 0; }
.target-trigger { min-height: 40px; height: auto; text-align: left; }
.target-trigger :deep(> span) { display: flex; gap: 10px; width: 100%; min-width: 0; }
.target-trigger :deep(> span > span) { flex: 1; white-space: normal; overflow-wrap: anywhere; }
.target-trigger svg { flex: 0 0 auto; }
.folder-footer { display: flex; gap: 12px; flex-wrap: wrap; justify-content: flex-end; }
.folder-footer :deep(.el-button + .el-button) { margin-left: 0; }
</style>
