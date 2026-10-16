<script setup lang="ts">
// Shared visual navigation; provider adapters retain their own account and node identities.
import { ArrowUp, ChevronRight, Folder, RefreshCw } from "lucide-vue-next";
export interface DirectoryNode { id: string; name: string; disabled?: boolean; reason?: string | null }
defineProps<{
  rootLabel: string; pathLabel: string; currentName: string | null; parentId: string | null;
  ancestors: DirectoryNode[]; items: DirectoryNode[]; loading: boolean; problem?: string;
  pending?: boolean; nextCursor?: string | null; selectable: boolean; selectionReason?: string | null;
}>();
const emit = defineEmits<{ navigate: [id?: string]; refresh: []; more: [] }>();
</script>
<template>
  <section class="folder-browser" :aria-busy="loading">
    <nav class="folder-breadcrumbs" :aria-label="pathLabel">
      <button type="button" :disabled="loading" @click="emit('navigate')">{{ rootLabel }}</button>
      <template v-for="node in ancestors" :key="node.id"><span aria-hidden="true">/</span><button type="button" :disabled="loading" @click="emit('navigate', node.id)">{{ node.name }}</button></template>
      <template v-if="currentName"><span aria-hidden="true">/</span><span aria-current="page">{{ currentName }}</span></template>
    </nav>
    <div class="folder-toolbar">
      <el-button :disabled="loading || parentId === null" @click="emit('navigate', parentId ?? undefined)"><ArrowUp :size="15" />上一级</el-button>
      <el-button :loading="loading" @click="emit('refresh')"><RefreshCw :size="15" />刷新目录</el-button>
    </div>
    <p v-if="loading" role="status">正在读取文件夹…</p>
    <div v-else-if="problem" role="alert"><p>{{ problem }}</p><el-button @click="emit('refresh')">重试</el-button></div>
    <p v-else-if="pending" role="status">正在获取云盘目录，稍后刷新查看。</p>
    <template v-else>
      <ul class="folder-children" aria-label="子文件夹">
        <li v-for="folder in items" :key="folder.id">
          <button type="button" :disabled="folder.disabled" @click="emit('navigate', folder.id)"><Folder :size="18" aria-hidden="true" /><span>{{ folder.name }}</span><ChevronRight :size="16" aria-hidden="true" /></button>
          <small v-if="folder.reason">{{ folder.reason }}</small>
        </li>
      </ul>
      <p v-if="!items.length" class="folder-empty">此文件夹内没有子文件夹。</p>
      <el-button v-if="nextCursor" @click="emit('more')">加载更多文件夹</el-button>
      <p v-if="!selectable && selectionReason" role="alert">{{ selectionReason }}</p>
    </template>
    <p class="folder-current">当前文件夹：<strong><slot name="current">{{ currentName || rootLabel }}</slot></strong></p>
  </section>
</template>
<style scoped>
.folder-browser { display: grid; gap: 12px; font-size: 14px; }
.folder-breadcrumbs { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; overflow-wrap: anywhere; }
.folder-breadcrumbs button { background: none; border: 0; color: var(--el-color-primary); padding: 4px; font: inherit; cursor: pointer; }
.folder-toolbar { display: flex; gap: 12px; flex-wrap: wrap; }
.folder-toolbar :deep(.el-button + .el-button) { margin-left: 0; }
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
