<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from "vue";
import { ArrowUp, ChevronRight, Folder, RefreshCw } from "lucide-vue-next";
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
      <section class="folder-browser" :aria-busy="loading">
        <nav class="folder-breadcrumbs" aria-label="夸克文件夹路径">
          <button type="button" :disabled="loading" @click="load()">我的云盘</button>
          <template v-for="node in ancestors" :key="node.id"><span aria-hidden="true">/</span><button type="button" :disabled="loading" @click="load(node.id)">{{ node.name }}</button></template>
          <template v-if="page?.parent?.parentId !== null && page?.parent"><span aria-hidden="true">/</span><span aria-current="page">{{ page.parent.name }}</span></template>
        </nav>
        <div class="folder-toolbar">
          <el-button :disabled="loading || !page?.parent?.parentId" @click="load(page?.parent?.parentId ?? undefined)"><ArrowUp :size="15" />上一级</el-button>
          <el-button :loading="loading" @click="load(requestedId)"><RefreshCw :size="15" />刷新目录</el-button>
        </div>
        <p v-if="loading" role="status">正在读取文件夹…</p>
        <div v-else-if="error" role="alert"><p>{{ error }}</p><el-button @click="load(requestedId)">重试</el-button></div>
        <div v-else-if="page?.status === 'LOADING'" role="status"><p>正在获取云盘目录，稍后刷新查看。</p></div>
        <p v-else-if="page?.status === 'NEEDS_ACTION'" role="alert">{{ page.parent?.availabilityReason || "目录暂不可用，请检查账号或返回上级重新选择。" }}</p>
        <template v-else-if="page?.status === 'READY'">
          <ul class="folder-children" aria-label="子文件夹">
            <li v-for="folder in page.items" :key="folder.id">
              <button type="button" :disabled="folder.availability !== 'AVAILABLE'" @click="load(folder.id)">
                <Folder :size="18" aria-hidden="true" /><span>{{ folder.name }}</span><ChevronRight :size="16" aria-hidden="true" />
              </button>
              <small v-if="folder.availabilityReason">{{ folder.availabilityReason }}</small>
            </li>
          </ul>
          <p v-if="!page.items.length" class="folder-empty">此文件夹内没有子文件夹。</p>
          <el-button v-if="page.nextCursor" @click="load(requestedId, true)">加载更多文件夹</el-button>
          <p v-if="page.parent && !page.parent.selectable" role="alert">{{ page.parent.availabilityReason || "当前文件夹不可作为转存目标。" }}</p>
        </template>
        <p class="folder-current">当前文件夹：<strong>{{ page ? (currentPath || "我的云盘") : "尚未读取" }}</strong></p>
      </section>
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
.folder-browser { display: grid; gap: 12px; font-size: 14px; }
.folder-breadcrumbs { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; overflow-wrap: anywhere; }
.folder-breadcrumbs button { background: none; border: 0; color: var(--el-color-primary); padding: 4px; font: inherit; cursor: pointer; }
.folder-toolbar, .folder-footer { display: flex; gap: 12px; flex-wrap: wrap; }
.folder-footer { justify-content: flex-end; }
.folder-footer :deep(.el-button + .el-button), .folder-toolbar :deep(.el-button + .el-button) { margin-left: 0; }
.folder-children { list-style: none; padding: 0; margin: 0; max-height: min(320px, 36vh); overflow-y: auto; }
.folder-children button { display: flex; gap: 12px; align-items: center; width: 100%; padding: 12px; background: var(--el-bg-color); color: var(--el-text-color-primary); border: 0; border-bottom: 1px solid var(--el-border-color-lighter); text-align: left; font: inherit; cursor: pointer; }
.folder-children button:hover { background: var(--el-fill-color-light); }
.folder-children button:disabled { color: var(--el-text-color-disabled); cursor: not-allowed; }
.folder-children button span { flex: 1; overflow-wrap: anywhere; }
.folder-children svg { flex: 0 0 auto; }
.folder-children small { display: block; padding: 0 12px 8px; }
.folder-current { margin: 0; padding-top: 12px; border-top: 1px solid var(--el-border-color-lighter); overflow-wrap: anywhere; }
.folder-empty { color: var(--el-text-color-secondary); }
button:focus-visible { outline: 2px solid var(--el-color-primary); outline-offset: 2px; }
</style>
