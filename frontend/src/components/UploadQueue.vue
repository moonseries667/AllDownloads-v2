<script setup lang="ts">
import { CloudUpload } from "lucide-vue-next";
import { formatBytes, formatDate, platformName } from "../format";
import StatusBadge from "./StatusBadge.vue";
import { uploadActionLabel, uploadPercent, uploadStateLabel, type UploadAction, type UploadRow } from "./upload-queue";
defineProps<{ items: UploadRow[]; busyId?: string | null; emptyText?: string }>();
const emit = defineEmits<{ action: [row: UploadRow, action: UploadAction] }>();
</script>
<template>
  <ul v-if="items.length" class="upload-queue" aria-label="上传队列">
    <li v-for="row in items" :key="row.id" class="upload-row" :data-upload-id="row.id">
      <CloudUpload class="upload-icon" :size="22" aria-hidden="true" />
      <div class="upload-name"><strong>{{ row.title }}</strong><small>{{ platformName[row.platform] || row.platform }} · {{ row.instanceLabel }}</small><small v-if="row.updatedAt">更新于 {{ formatDate(row.updatedAt) }}</small></div>
      <div class="upload-state">
        <StatusBadge :state="row.state" :label="uploadStateLabel(row)" />
        <template v-if="['QUEUED', 'UPLOADING', 'RUNNING', 'VERIFYING', 'PAUSING'].includes(row.state)">
          <progress v-if="uploadPercent(row) !== null" :value="uploadPercent(row)!" max="100" :aria-label="row.title + '传输进度'" />
          <p v-else class="upload-fact">进度总量待确认</p>
          <small v-if="row.totalBytes !== null">{{ formatBytes(row.doneBytes) }} / {{ formatBytes(row.totalBytes) }}</small>
          <small v-else-if="row.totalItems !== null">{{ row.doneItems }} / {{ row.totalItems }} 个文件</small>
        </template>
        <p v-if="row.error" class="upload-error">{{ row.error }}</p>
      </div>
      <div class="upload-destination"><small>Google Drive</small><span>{{ row.destination || '目标待确认' }}</span></div>
      <div class="upload-actions"><el-button v-for="action in row.actions" :key="action" :disabled="!!busyId || row.desiredAction === 'PAUSE' && row.state !== 'PAUSED'" :loading="busyId === row.id" @click="emit('action', row, action)">{{ uploadActionLabel[action] }}</el-button></div>
    </li>
  </ul>
  <el-empty v-else :description="emptyText || '暂无上传记录'" :image-size="64" />
</template>
<style scoped>
.upload-queue { list-style: none; margin: 0; padding: 0; }
.upload-row { display: grid; grid-template-columns: 24px minmax(180px, 1.5fr) minmax(150px, 1fr) minmax(150px, 1fr) minmax(100px, auto); gap: 18px; padding: 20px 24px; align-items: start; border-top: 1px solid var(--el-border-color-lighter); }
.upload-icon { color: var(--el-text-color-secondary); margin-top: 3px; }
.upload-name, .upload-state, .upload-destination { display: grid; gap: 7px; min-width: 0; overflow-wrap: anywhere; }
.upload-name strong { font-size: 15px; font-weight: 600; }
small, .upload-fact { color: var(--el-text-color-secondary); font-size: 12px; }
.upload-state .status-badge { justify-self: start; }
.upload-state progress { width: 100%; height: 6px; accent-color: var(--el-color-primary); }
.upload-error { color: var(--el-color-danger); margin: 0; font-size: 13px; }
.upload-fact { margin: 0; }
.upload-actions { display: flex; justify-content: flex-end; flex-wrap: wrap; gap: 8px; max-width: 210px; }
.upload-actions :deep(.el-button + .el-button) { margin: 0; }
@media (max-width: 1200px) { .upload-row { gap: 12px; grid-template-columns: minmax(180px, 1.3fr) minmax(130px, 1fr) minmax(130px, 1fr) 100px; padding: 18px; } .upload-icon { display: none; } }
</style>
