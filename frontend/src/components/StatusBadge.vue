<script setup lang="ts">
import { computed } from "vue";
const props = defineProps<{ state: string; label?: string }>();
const labels: Record<string, string> = {
  NOT_REQUESTED: "未下载",
  QUEUED: "等待中",
  RUNNING: "进行中",
  DOWNLOADING: "下载中",
  UPLOADING: "上传中",
  VERIFYING: "待云端核对",
  VERIFIED: "已验证",
  SUCCEEDED: "已完成",
  PARTIAL: "部分完成",
  NEEDS_ACTION: "需要处理",
  FAILED: "失败",
  CANCELLED: "已取消",
  PAUSED: "已暂停",
  PAUSING: "正在暂停",
  WAITING_RETRY: "等待重试",
  READY: "可用",
  AVAILABLE: "可用",
  REMOVED: "已移出来源",
  UNAVAILABLE: "不可用",
  UNKNOWN: "未知",
  ERROR: "异常",
  MISSING: "无本机文件",
  CLEANED: "本机已清理",
  ACTIVE: "已连接",
  AUTH_REQUIRED: "待登录",
  UPDATED: "已转存",
  PREPARING: "准备中",
  EXPIRED: "已过期",
};
const tone = computed(() =>
  ["VERIFIED", "SUCCEEDED", "ACTIVE", "UPDATED", "AVAILABLE", "READY"].includes(
    props.state,
  )
    ? "good"
    : ["FAILED", "NEEDS_ACTION", "AUTH_REQUIRED", "UNAVAILABLE"].includes(
          props.state,
        )
      ? "bad"
      : ["RUNNING", "DOWNLOADING", "UPLOADING", "PREPARING"].includes(
            props.state,
          )
        ? "active"
        : ["PARTIAL", "VERIFYING", "WAITING_RETRY", "PAUSING"].includes(
              props.state,
            )
          ? "warn"
          : "muted",
);
</script>
<template>
  <span class="status-badge" :class="tone"
    ><span aria-hidden="true" class="status-dot"></span
    >{{ label || labels[state] || state }}</span
  >
</template>
