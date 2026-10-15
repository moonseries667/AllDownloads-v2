<script setup lang="ts">
import { computed, onBeforeUnmount, onDeactivated, ref, watch } from "vue";
import { ChevronRight, Folder } from "lucide-vue-next";
import DirectoryBrowser from "./DirectoryBrowser.vue";
import { archiveSettingsApi, errorMessage, type DriveFolderPage, type DriveTarget } from "../api/archive-settings";
const props = defineProps<{ modelValue: DriveTarget | null; accountId: string; disabled?: boolean; loadFolders?: typeof archiveSettingsApi.folders }>();
const emit = defineEmits<{ "update:modelValue": [target: DriveTarget] }>();
const visible = ref(false), loading = ref(false), error = ref("");
const page = ref<DriveFolderPage | null>(null), requestedId = ref<string>();
let sequence = 0;
const root = computed(() => page.value?.breadcrumbs.find(node => node.parentId === null)?.name || (page.value?.parent?.parentId === null ? page.value.parent.name : "Google Drive"));
const ancestors = computed(() => page.value?.breadcrumbs.filter(node => node.parentId !== null && node.id !== page.value?.parent?.id) ?? []);
const current = computed(() => {
  const parent = page.value?.parent;
  return !loading.value && !error.value && page.value?.status === "READY" && parent?.accountId === props.accountId && parent.selectable && parent.availability === "AVAILABLE" ? parent : null;
});
async function load(parentId?: string, append = false) {
  if (!props.accountId) return;
  const request = ++sequence, accountId = props.accountId, previous = page.value;
  requestedId.value = parentId; loading.value = true; error.value = "";
  if (!append) page.value = null;
  try {
    const read = () => (props.loadFolders || archiveSettingsApi.folders)(accountId, parentId, append ? previous?.nextCursor ?? undefined : undefined);
    let result = await read();
    for (let attempt = 0; result.status === "LOADING" && attempt < 40; attempt++) {
      if (request !== sequence || !visible.value || accountId !== props.accountId) return;
      await new Promise(resolve => setTimeout(resolve, 1500));
      if (request !== sequence || !visible.value || accountId !== props.accountId) return;
      result = await read();
    }
    if (request !== sequence || !visible.value || accountId !== props.accountId) return;
    if (result.parent && result.parent.accountId !== accountId || result.items.some(item => item.accountId !== accountId)) throw Error("目录响应与当前 Google Drive 账号不一致，请重新读取。");
    page.value = append && previous?.parent?.id === result.parent?.id && previous?.parent?.revision === result.parent?.revision
      ? { ...result, items: [...new Map([...previous!.items, ...result.items].map(node => [node.id, node])).values()] } : result;
    if (result.status === "NEEDS_ACTION") error.value = errorMessage(result.error, result.parent?.availabilityReason || "目录不可用，请返回根目录或检查账号权限。");
  } catch (cause) { if (request === sequence && visible.value) error.value = cause instanceof Error ? cause.message : "目录读取失败，请重试。"; }
  finally { if (request === sequence) loading.value = false; }
}
function open() { visible.value = true; void load(props.modelValue?.accountId === props.accountId ? props.modelValue.folderId : undefined); }
function close() { sequence++; loading.value = false; }
function select() {
  const folder = current.value;
  if (!folder) return;
  emit("update:modelValue", { accountId: folder.accountId, folderId: folder.id, revision: folder.revision, name: folder.name, path: folder.path });
  visible.value = false;
}
watch(visible, value => { if (!value) close(); });
watch(() => props.accountId, () => { close(); page.value = null; if (visible.value) void load(); });
onBeforeUnmount(close);
onDeactivated(() => { visible.value = false; close(); });
</script>
<template>
  <div class="drive-target-field">
    <el-button class="drive-target-trigger" aria-label="Goole Drive上传目录" :disabled="disabled || !accountId" @click="open"><Folder :size="17" aria-hidden="true" /><span>{{ modelValue && modelValue.accountId === accountId ? (modelValue.path || modelValue.name) : '选择 Google Drive 文件夹' }}</span><ChevronRight :size="17" aria-hidden="true" /></el-button>
    <el-dialog v-model="visible" title="选择 Google Drive 上传目录" class="drive-folder-dialog" width="min(620px, calc(100vw - 48px))" append-to-body destroy-on-close>
      <DirectoryBrowser :root-label="root" path-label="Google Drive 文件夹路径" :current-name="page?.parent?.parentId ? page.parent.name : null" :parent-id="page?.parent?.parentId ?? null" :ancestors="ancestors" :items="page?.items.map(folder => ({ id: folder.id, name: folder.name, disabled: folder.availability !== 'AVAILABLE', reason: folder.availabilityReason })) ?? []" :loading="loading" :problem="error" :pending="page?.status === 'LOADING'" :next-cursor="page?.nextCursor" :selectable="!!current" :selection-reason="page?.parent?.availabilityReason || '此文件夹不能作为上传目标。'" @navigate="load($event)" @refresh="load(requestedId)" @more="load(requestedId, true)"><template #current>{{ page?.parent ? (page.parent.path || page.parent.name) : '尚未读取' }}</template></DirectoryBrowser>
      <template #footer><div class="drive-folder-footer"><el-button @click="visible = false">取消</el-button><el-button type="primary" :disabled="!current" @click="select">使用此文件夹</el-button></div></template>
    </el-dialog>
  </div>
</template>
<style scoped>
:global(.el-dialog.drive-folder-dialog) { margin: 24px auto; max-height: calc(100dvh - 48px); display: flex; flex-direction: column; }
:global(.drive-folder-dialog .el-dialog__body) { min-height: 0; overflow-y: auto; }
:global(.drive-folder-dialog .el-dialog__header), :global(.drive-folder-dialog .el-dialog__footer) { flex-shrink: 0; }
.drive-target-field, .drive-target-trigger { width: 100%; min-width: 0; }
.drive-target-trigger { min-height: 40px; height: auto; padding-block: 10px; text-align: left; }
.drive-target-trigger :deep(> span) { display: flex; gap: 10px; width: 100%; min-width: 0; }
.drive-target-trigger :deep(> span > span) { flex: 1; white-space: normal; overflow-wrap: anywhere; }
.drive-target-trigger svg { flex: 0 0 auto; }
.drive-folder-footer { display: flex; justify-content: flex-end; flex-wrap: wrap; gap: 12px; }
.drive-folder-footer :deep(.el-button + .el-button) { margin-left: 0; }
</style>
