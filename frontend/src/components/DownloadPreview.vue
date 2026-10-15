<script setup lang="ts">
import { ref, watch, onBeforeUnmount } from "vue";
import { Download, FolderOpen, RefreshCw } from "lucide-vue-next";
import { api, notifySuccess } from "../api/client";
import type { Platform, Selection, Preview } from "../api/types";
import { formatBytes } from "../format";
const props = defineProps<{
  modelValue: boolean;
  platform: Platform;
  selection: Selection;
}>();
const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  submitted: [];
}>();
const preview = ref<Preview | null>(null),
  busy = ref(false),
  submitting = ref(false),
  error = ref(""),
  root = ref(""),
  invalid = ref(false);
let generation = 0,
  timer: ReturnType<typeof setTimeout> | undefined;
async function prepare() {
  const g = ++generation;
  busy.value = true;
  error.value = "";
  invalid.value = false;
  preview.value = null;
  clearTimeout(timer);
  try {
    const selection = JSON.parse(JSON.stringify(props.selection));
    const [receipt, settings] = await Promise.all([
      api.post("/download-previews", { platform: props.platform, selection }),
      api.get("/settings"),
    ]);
    if (g !== generation) return;
    root.value = settings.downloadRoot;
    const read = async () => {
      try {
        const data = await api.get<Preview>(
          "/download-previews/" + receipt.previewId,
        );
        if (g !== generation) return;
        preview.value = data;
        if (data.status === "PREPARING") timer = setTimeout(read, 2000);
        else busy.value = false;
      } catch (e) {
        if (g === generation) {
          error.value = (e as Error).message;
          busy.value = false;
        }
      }
    };
    await read();
  } catch (e) {
    if (g === generation) {
      error.value = (e as Error).message;
      busy.value = false;
    }
  }
}
async function submit() {
  if (!preview.value) return;
  submitting.value = true;
  error.value = "";
  try {
    await api.post("/download-orders", { previewId: preview.value.id });
    notifySuccess("下载订单已排队。可在下载管理中查看；尚未下载完成。");
    emit("submitted");
    emit("update:modelValue", false);
  } catch (e) {
    error.value = (e as Error).message;
    invalid.value = true;
  } finally {
    submitting.value = false;
  }
}
watch(
  () => props.modelValue,
  (value) => {
    if (value) prepare();
    else {
      generation++;
      clearTimeout(timer);
    }
  },
);
onBeforeUnmount(() => {
  generation++;
  clearTimeout(timer);
});
</script>
<template>
  <el-dialog
    :model-value="modelValue"
    title="确认下载清单"
    width="780px"
    @update:model-value="emit('update:modelValue', $event)"
    :close-on-click-modal="false"
  >
    <p class="dialog-intro">
      仅处理本次预览中的文件。之后新增的内容不会自动加入。
    </p>
    <div class="path-banner">
      <FolderOpen :size="20" />
      <div>
        <span>统一本机下载根</span><strong>{{ root || "读取中…" }}</strong>
      </div>
    </div>
    <el-alert
      v-if="error"
      :title="error"
      type="error"
      :closable="false"
      show-icon
      class="mb16"
    />
    <div v-loading="busy" style="min-height: 120px">
      <template v-if="preview"
        ><div class="preview-summary">
          <span
            ><b>{{ preview.summary.download }}</b> 个待下载</span
          ><span>{{ preview.summary.reuse }} 个复用</span
          ><span>{{ preview.summary.blocked }} 个无法处理</span>
        </div>
        <el-table :data="preview.items" max-height="330"
          ><el-table-column label="文件与相对目录" min-width="360"
            ><template #default="{ row }"
              ><strong>{{ row.title }}</strong>
              <div class="path-text">{{ row.relativePath }}</div></template
            ></el-table-column
          ><el-table-column label="大小" width="100"
            ><template #default="{ row }">{{
              formatBytes(row.sizeBytes)
            }}</template></el-table-column
          ><el-table-column label="处理方式" width="115"
            ><template #default="{ row }"
              ><el-tooltip :content="row.reason || '提交后进入下载队列'"
                ><span
                  :class="row.decision === 'BLOCKED' ? 'text-danger' : ''"
                  >{{
                    {
                      DOWNLOAD: "下载",
                      REUSE: "复用 / 跳过",
                      BLOCKED: "无法处理",
                    }[row.decision as string]
                  }}</span
                ></el-tooltip
              ></template
            ></el-table-column
          ></el-table
        >
        <p class="muted" v-for="w in preview.warnings" :key="w">
          {{ w }}
        </p></template
      >
    </div>
    <template #footer
      ><el-button @click="emit('update:modelValue', false)">返回选择</el-button
      ><el-button :icon="RefreshCw" @click="prepare" :loading="busy"
        >重新预览</el-button
      ><el-button
        type="primary"
        :icon="Download"
        :loading="submitting"
        :disabled="
          busy ||
          invalid ||
          preview?.status !== 'READY' ||
          !preview?.summary.download
        "
        @click="submit"
        >提交下载</el-button
      ></template
    >
  </el-dialog>
</template>
