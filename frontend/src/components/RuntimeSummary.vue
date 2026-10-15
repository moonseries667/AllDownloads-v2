<script setup lang="ts">
import { computed } from "vue";
import { Activity, HardDrive } from "lucide-vue-next";
import { formatDate } from "../format";
import { serviceSummary, serviceLabels, mountLabels, type RuntimeFacts } from "./runtimePresentation";
const props = defineProps<{ runtime?: RuntimeFacts | null; loading?: boolean; error?: string; demo?: boolean }>();
const summary = computed(() => serviceSummary(props.error ? null : props.runtime));
</script>
<template>
  <section class="runtime-summary" aria-label="运行摘要" aria-live="polite" :aria-busy="loading">
    <div class="runtime-item">
      <Activity :size="22" />
      <div><h1 class="page-title" :class="'text-' + summary.tone">{{ loading ? '正在查询运行状态…' : summary.label }}</h1>
        <p v-if="error" class="text-danger">{{ error }} · 状态待重新确认</p>
        <p v-else-if="runtime?.services?.length" class="service-list"><span v-for="service in runtime.services" :key="service.id">{{ service.id }}：{{ serviceLabels[service.state] || '未知' }}</span></p>
        <p v-else>尚无可判定的服务观测结果</p>
      </div>
    </div>
    <div class="runtime-item"><HardDrive :size="22" /><div>
      <strong>{{ loading ? '正在查询挂载…' : error ? '挂载状态未知' : mountLabels[runtime?.mount?.state || 'UNKNOWN'] || '挂载状态未知' }}</strong>
      <p>挂载状态与云端归档核验分别记录</p>
    </div></div>
    <div class="runtime-observed"><span v-if="demo">隔离演示数据</span><span>观测时间：{{ formatDate(runtime?.observedAt) }}</span></div>
  </section>
</template>
