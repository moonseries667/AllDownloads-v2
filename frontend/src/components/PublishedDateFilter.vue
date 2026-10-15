<script setup lang="ts">
import { computed, ref } from "vue";
const props = defineProps<{ from: string; to: string }>();
const emit = defineEmits<{ "update:from": [value: string]; "update:to": [value: string] }>();
const start = ref(), end = ref();
const invalid = computed(() => Boolean(props.from && props.to && props.from > props.to));
</script>
<template>
  <div class="published-date-filter">
    <span class="filter-label">发布于</span>
    <el-date-picker ref="start" :model-value="from" type="date" value-format="YYYY-MM-DD" format="YYYY-MM-DD" placeholder="开始日期" aria-label="发布时间起始日期" :clearable="true" popper-class="published-calendar" @update:model-value="emit('update:from', $event || '')" @keydown.esc="start?.handleClose(); start?.focus()" />
    <span aria-hidden="true">—</span>
    <el-date-picker ref="end" :model-value="to" type="date" value-format="YYYY-MM-DD" format="YYYY-MM-DD" placeholder="结束日期" aria-label="发布时间结束日期" :clearable="true" popper-class="published-calendar" @update:model-value="emit('update:to', $event || '')" @keydown.esc="end?.handleClose(); end?.focus()" />
    <el-button link :disabled="!from && !to" @click="emit('update:from', ''); emit('update:to', '')">清除日期</el-button>
    <span class="date-zone">上海时间</span>
    <span v-if="invalid" class="text-danger" role="alert">结束日期不能早于开始日期</span>
  </div>
</template>
