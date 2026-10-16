<script setup lang="ts">
import { onBeforeUnmount, onDeactivated, ref } from "vue";
import { FolderOpen } from "lucide-vue-next";
export type NativeSelection = { status: "SELECTED"; path: string; reference: string } | { status: "CANCELLED" } | { status: "UNAVAILABLE"; reason: string };
const props = defineProps<{
  modelValue: string; disabled?: boolean; selectDirectory: (signal: AbortSignal) => Promise<NativeSelection>;
}>();
const emit = defineEmits<{ selected: [selection: Extract<NativeSelection, { status: "SELECTED" }>]; busy: [value: boolean] }>();
const selecting = ref(false), message = ref(""), failed = ref(false);
let sequence = 0;
let controller: AbortController | null = null;
async function select() {
  if (selecting.value) return;
  const request = ++sequence;
  controller = new AbortController();
  emit("busy", true);
  selecting.value = true; message.value = "正在运行主机上打开系统文件夹选择窗口…"; failed.value = false;
  try {
    const result = await props.selectDirectory(controller.signal);
    if (request !== sequence) return;
    if (result.status === "SELECTED") {
      emit("selected", result);
      message.value = "已选择，保存设置后生效。";
    } else if (result.status === "CANCELLED") message.value = "已取消选择，原目录保持不变。";
    else { message.value = result.reason; failed.value = true; }
  } catch (error) {
    if (request !== sequence) return;
    message.value = error instanceof Error ? error.message : "系统目录选择不可用，请稍后重试。"; failed.value = true;
  } finally { if (request === sequence) { selecting.value = false; emit("busy", false); } }
}
function cancel() { controller?.abort(); message.value = "已请求取消选择，正在等待窗口关闭…"; }
function dispose() { controller?.abort(); sequence++; selecting.value = false; emit("busy", false); }
onBeforeUnmount(dispose);
onDeactivated(dispose);
</script>
<template>
  <div class="native-directory-field" :aria-busy="selecting">
    <el-button class="native-directory-trigger" aria-label="选择统一本机下载根目录" :disabled="disabled || selecting" :loading="selecting" @click="select"><FolderOpen v-if="!selecting" :size="17" aria-hidden="true" /><span>{{ modelValue || '选择运行主机上的文件夹' }}</span></el-button>
    <el-button v-if="selecting" class="cancel-selection" @click="cancel">取消选择</el-button>
    <p class="field-help">选择产品运行主机的本机目录。更改仅在校验并保存后生效，已有订单继续使用原目标。</p>
    <p v-if="message" :role="failed ? 'alert' : 'status'" :class="{ 'text-danger': failed }">{{ message }}</p>
  </div>
</template>
<style scoped>
.native-directory-field { width: 100%; min-width: 0; }
.native-directory-trigger { width: 100%; min-height: 40px; height: auto; padding-block: 10px; text-align: left; }
.native-directory-trigger :deep(> span) { width: 100%; display: flex; gap: 10px; white-space: normal; overflow-wrap: anywhere; }
.native-directory-trigger svg { flex: 0 0 auto; }
.native-directory-field p { margin: 8px 0 0; line-height: 1.6; }
.cancel-selection { margin: 8px 0 0; }
</style>
