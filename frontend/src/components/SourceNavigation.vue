<script setup lang="ts">
import { computed } from "vue";
import { Library, FolderHeart, Layers, Clapperboard, Pin, Settings2 } from "lucide-vue-next";
import type { RecordData } from "../api/types";
const props = defineProps<{ sources: RecordData[]; modelValue: string }>();
const emit = defineEmits<{ "update:modelValue": [id: string]; edit: [source: RecordData] }>();
const groups = computed(() => [
  { label: "收藏夹", kinds: ["FAVORITE"] },
  { label: "合集", kinds: ["COLLECTION"] },
  { label: "其他", kinds: ["USER_ADDED", "MOVIE", "SERIES"] },
].map(group => ({ ...group, sections: group.kinds.map(kind => ({
  kind,
  label: ({ USER_ADDED: "用户添加视频", MOVIE: "电影", SERIES: "番剧" } as Record<string, string>)[kind],
  rows: props.sources.filter(s => s.kind === kind).sort((a, b) => Number(b.pinned) - Number(a.pinned)),
})) })));
</script>
<template>
  <nav class="source-panel" aria-label="Bilibili 来源">
    <div class="source-heading">我的来源<span>{{ sources.length }}</span></div>
    <button class="source-link" :class="{ selected: !modelValue }" :aria-current="!modelValue ? 'page' : undefined" @click="emit('update:modelValue', '')">
      <Library :size="17" /><span>全部内容</span>
    </button>
    <section v-for="group in groups" :key="group.label" class="source-category" :aria-label="group.label">
      <h3 class="source-group">{{ group.label }}</h3>
      <section v-for="section in group.sections" :key="section.kind" :aria-label="section.label">
        <h4 v-if="section.label" class="source-subgroup">{{ section.label }}</h4>
        <div v-for="s in section.rows" :key="s.id" class="source-row">
          <button class="source-link" :class="{ selected: modelValue === s.id }" :aria-current="modelValue === s.id ? 'page' : undefined" @click="emit('update:modelValue', s.id)">
            <component :is="section.kind === 'FAVORITE' ? FolderHeart : ['SERIES', 'MOVIE'].includes(section.kind) ? Clapperboard : Layers" :size="16" />
            <span>{{ s.alias || s.title }}</span><Pin v-if="s.pinned" :size="12" aria-label="已置顶" />
          </button>
          <button class="source-edit icon-button" :aria-label="'设置来源 ' + s.title" @click="emit('edit', s)"><Settings2 :size="14" /></button>
        </div>
        <p v-if="!section.rows.length" class="source-empty">尚未添加</p>
      </section>
    </section>
    <p class="source-hint">添加和刷新来源只收录信息，下载由你决定。</p>
  </nav>
</template>
