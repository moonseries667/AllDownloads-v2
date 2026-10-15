<script setup lang="ts">
import { watch } from "vue";
import { Bell } from "lucide-vue-next";
import { notificationStore, type ServerIssue } from "../notifications/store";
import { formatDate } from "../format";

const props = defineProps<{ issues: ServerIssue[] }>();
watch(() => props.issues, issues => notificationStore.replaceServerIssues(issues), { immediate: true });
const { attention } = notificationStore;
</script>

<template>
  <el-popover placement="bottom-end" width="390" trigger="click">
    <template #reference>
      <button class="icon-button notification-button" aria-label="通知">
        <Bell :size="19" aria-hidden="true" />
        <span v-if="attention.length" class="notification-dot"></span>
      </button>
    </template>
    <div class="notification-center">
      <h3>需要关注 <span v-if="attention.length">{{ attention.length }}</span></h3>
      <p v-if="!attention.length" class="muted">当前没有待处理问题</p>
      <div v-else class="attention-list">
        <router-link v-for="notice in attention" :key="notice.id" :to="notice.href" class="attention-notice">
          <strong>{{ notice.title }}</strong>
          <span>{{ notice.message }}</span>
          <small>{{ notice.severity === 'warning' ? '警告' : notice.severity === 'error' ? '错误' : '提示' }} · {{ formatDate(notice.occurredAt) }}</small>
        </router-link>
      </div>
      <p v-if="attention.length" class="attention-hint">关闭提示或查看记录不会解决问题。</p>
    </div>
  </el-popover>
</template>

<style scoped>
.notification-center h3 { margin: 4px 0 12px; font-size: 16px; }
.notification-center h3 span { color: var(--muted, #62756d); font-size: 13px; }
.attention-list { max-height: min(480px, 65vh); overflow-y: auto; }
.attention-notice { display: grid; gap: 6px; padding: 12px 4px; border-bottom: 1px solid var(--border, #e2e9e6); color: inherit; text-decoration: none; overflow-wrap: anywhere; }
.attention-notice:hover { background: var(--surface-soft, #f4f8f6); }
.attention-notice:focus-visible { outline: 2px solid var(--primary, #166a59); outline-offset: -2px; }
.attention-notice strong { font-size: 14px; }
.attention-notice span { font-size: 13px; line-height: 1.6; }
.attention-notice small, .attention-hint { color: var(--muted, #62756d); font-size: 12px; }
.attention-hint { margin: 12px 0 2px; }
</style>
<style>
.notice-dismiss-body { display: block; padding: 0; width: 100%; border: 0; background: none; color: inherit; font: inherit; text-align: left; white-space: pre-wrap; overflow-wrap: anywhere; cursor: pointer; }
.notice-dismiss-body:focus-visible, .notice-action:focus-visible { outline: 2px solid var(--primary, #166a59); outline-offset: 3px; }
.notice-action { margin-top: 8px; color: var(--primary, #166a59); font: inherit; cursor: pointer; }
</style>
