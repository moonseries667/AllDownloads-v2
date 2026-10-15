<script setup lang="ts">
import { ref } from "vue";
import NativeDirectoryField, { type NativeSelection } from "../../src/components/NativeDirectoryField.vue";
import UploadQueue from "../../src/components/UploadQueue.vue";
import DriveFolderPicker from "../../src/components/DriveFolderPicker.vue";
import { driveFolderFixtures, driveTarget } from "../../src/api/demo-archive-settings";
import type { DriveFolderPage, DriveTarget } from "../../src/api/archive-settings";
import type { UploadRow } from "../../src/components/upload-queue";
const path = ref("D:\\fixture-downloads"), mode = ref("cancel"), calls = ref(0), actions = ref<string[]>([]);
const drive = ref<DriveTarget | null>(driveTarget(driveFolderFixtures[1]!)), driveMode = ref("normal");
let fetches = 0;
async function loadFolders(accountId: string, parentId = "drive-root", cursor?: string): Promise<DriveFolderPage> {
  await new Promise(resolve => setTimeout(resolve, driveMode.value === "slow" ? 450 : 30));
  if (driveMode.value === "denied") throw Error("Google Drive 访问权限不足");
  if (driveMode.value === "gone" && parentId !== "drive-root") throw Error("目录已失效，请返回根目录");
  if (driveMode.value === "fetching" && fetches++ === 0) return { parent: null, breadcrumbs: [], items: [], total: null, nextCursor: null, status: "LOADING", taskId: "fixture-listing", error: null };
  const parent = driveFolderFixtures.find(node => node.id === parentId)!;
  const breadcrumbs = [driveFolderFixtures[0]!, ...(parent.parentId && parent.parentId !== "drive-root" ? [driveFolderFixtures.find(node => node.id === parent.parentId)!] : []), ...(parent.parentId ? [parent] : [])];
  const all = driveFolderFixtures.filter(node => node.parentId === parentId), offset = Number(cursor || 0);
  return { parent, breadcrumbs, items: all.slice(offset, offset + 1).map(node => ({ ...node, accountId })), total: all.length, nextCursor: offset + 1 < all.length ? String(offset + 1) : null, status: "READY", taskId: null, error: null };
}
async function selectDirectory(): Promise<NativeSelection> {
  calls.value++;
  await new Promise(resolve => setTimeout(resolve, 120));
  if (mode.value === "cancel") return { status: "CANCELLED" };
  if (mode.value === "unavailable") return { status: "UNAVAILABLE", reason: "运行主机没有可用的交互桌面。" };
  if (mode.value === "failure") throw new Error("选择请求超时，请重试。");
  return { status: "SELECTED", path: "E:\\fixture-selected", reference: "fixture-selection-1" };
}
const rows = ref<UploadRow[]>([
  { id: "verify", title: "完整视频", platform: "BILIBILI", instanceLabel: "批次 1", state: "VERIFYING", desiredAction: "CONTINUE", destination: "我的云端硬盘/视频", doneBytes: "100", totalBytes: "100", doneItems: 1, totalItems: 1, actions: [], error: null, updatedAt: null },
  { id: "retry", title: "上传失败的视频", platform: "QUARK", instanceLabel: "批次 2", state: "NEEDS_ACTION", desiredAction: "CONTINUE", destination: "我的云端硬盘/资料", doneBytes: null, totalBytes: null, doneItems: 0, totalItems: null, actions: ["RETRY"], error: "远端暂时不可用", updatedAt: null },
]);
</script>
<template><main style="padding:24px;max-width:1200px;margin:auto"><h1>隔离替身测试</h1><label>选择结果 <select v-model="mode" aria-label="选择结果"><option value="cancel">取消</option><option value="success">成功</option><option value="unavailable">不可用</option><option value="failure">失败</option></select></label><NativeDirectoryField :model-value="path" :select-directory="selectDirectory" @selected="path = $event.path" /><p data-testid="calls">调用 {{ calls }} 次</p><label>Drive场景 <select v-model="driveMode" aria-label="Drive场景"><option value="normal">正常</option><option value="denied">权限不足</option><option value="gone">失效</option><option value="slow">延迟</option><option value="fetching">后台获取</option></select></label><DriveFolderPicker v-model="drive" account-id="g-main" :load-folders="loadFolders" /><p data-testid="drive-id">{{ drive?.folderId }}</p><UploadQueue :items="rows" @action="actions.push($event.id)" /><p data-testid="actions">{{ actions.join(',') }}</p></main></template>
