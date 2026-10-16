<script setup lang="ts">
import { ref } from "vue";
import QuarkFolderPicker from "../../src/components/QuarkFolderPicker.vue";
import { api } from "../../src/api/client";
import type { QuarkTarget } from "../../src/api/quark-folders";
import { demoQuarkFolderPage } from "../../src/api/quark-folder-fixtures";
const target = ref<QuarkTarget | null>(null);
const mode = new URLSearchParams(location.search).get("mode");
let calls = 0;
api.get = async <T,>(path: string): Promise<T> => {
  const query = new URL(path, location.origin).searchParams;
  calls++;
  if (mode === "error" && calls === 1) throw new Error("测试目录暂时无法读取");
  if (mode === "pages") query.set("pageSize", "1");
  const result = structuredClone(demoQuarkFolderPage(query));
  if (mode === "loading" && calls === 1) result.status = "LOADING";
  if (mode === "needs-action" && calls === 1) {
    result.status = "NEEDS_ACTION";
    result.parent!.availabilityReason = "目标已移动，请重新选择";
  }
  if (mode === "slow") await new Promise((resolve) => setTimeout(resolve, calls === 1 ? 800 : 10));
  return result as T;
};
</script>
<template><main style="padding:32px;max-width:600px"><QuarkFolderPicker v-model="target" account-id="q-main" label="测试转存目标" /><output aria-label="已选目标">{{ JSON.stringify(target) }}</output></main></template>
