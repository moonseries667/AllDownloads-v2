<!-- Adapted from Geeker-Admin SelectFilter (MIT © 2022 Halsey).
     Single-selection business filters; buttons replace clickable list items for keyboard support.
     See licenses/geeker-admin.txt. -->
<script setup lang="ts">
import { ref, watch } from "vue";
interface Option {
  value: string;
  label: string;
}
interface Filter {
  title: string;
  key: string;
  options: Option[];
}
const props = withDefaults(
  defineProps<{ data: Filter[]; defaultValues?: Record<string, string> }>(),
  { defaultValues: () => ({}) },
);
const selected = ref<Record<string, string>>({});
watch(
  () => props.defaultValues,
  () => {
    props.data.forEach(
      (item) =>
        (selected.value[item.key] = props.defaultValues[item.key] ?? ""),
    );
  },
  { deep: true, immediate: true },
);
const emit = defineEmits<{ change: [value: Record<string, string>] }>();
function select(item: Filter, option: Option) {
  selected.value[item.key] = option.value;
  emit("change", { ...selected.value });
}
</script>
<template>
  <div class="select-filter">
    <div v-for="item in data" :key="item.key" class="filter-line" role="group" :aria-label="item.title">
      <span class="filter-label" aria-hidden="true">{{ item.title }}</span>
      <div class="filter-options">
        <button
          v-for="option in item.options"
          :key="option.value"
          :aria-pressed="selected[item.key] === option.value"
          :class="{ selected: selected[item.key] === option.value }"
          @click="select(item, option)"
        >
          {{ option.label }}
        </button>
      </div>
    </div>
  </div>
</template>
