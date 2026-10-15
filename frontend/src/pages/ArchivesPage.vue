<script setup lang="ts">
import { computed, onActivated, onBeforeUnmount, onDeactivated, ref, watch } from "vue";
import { useRoute } from "vue-router";
import { RefreshCw, Search } from "lucide-vue-next";
import { dataRevision, notifyError, notifySuccess } from "../api/client";
import { archiveSettingsApi, archiveRow, type ArchiveUpload } from "../api/archive-settings";
import UploadQueue from "../components/UploadQueue.vue";
import { uploadGroup, uploadGroups, type UploadAction, type UploadRow } from "../components/upload-queue";
import SelectFilter from "../components/SelectFilter.vue";
const route = useRoute();
const items = ref<ArchiveUpload[]>([]), platform = ref(""), group = ref(""), q = ref("");
const loading = ref(false), error = ref(""), loaded = ref(false), nextCursor = ref<string | null>(null), total = ref<number | null>(null), busyId = ref<string | null>(null);
const rows = computed(() => items.value.map(archiveRow));
const visible = computed(() => rows.value.filter(row => (!group.value || uploadGroup(row.state) === group.value) && (!q.value || row.title.toLocaleLowerCase().includes(q.value.toLocaleLowerCase()))));
let sequence = 0, active = false, loadedPages = 1, timer: ReturnType<typeof setTimeout> | undefined;
async function load(append = false, refresh = false) {
  const request = ++sequence;
  loading.value = true;
  try {
    let result = await archiveSettingsApi.archives({ platform: platform.value, cursor: append ? nextCursor.value ?? undefined : undefined });
    const refreshed = [...result.items];
    if (refresh) for (let page = 1; page < loadedPages && result.nextCursor; page++) {
      result = await archiveSettingsApi.archives({ platform: platform.value, cursor: result.nextCursor });
      refreshed.push(...result.items);
    }
    if (request !== sequence) return;
    items.value = [...new Map((append ? [...items.value, ...result.items] : refreshed).map(item => [item.id, item])).values()];
    loadedPages = append ? loadedPages + 1 : refresh ? loadedPages : 1;
    nextCursor.value = result.nextCursor;
    total.value = result.total; error.value = ""; loaded.value = true;
  } catch (cause) { if (request === sequence) error.value = cause instanceof Error ? cause.message : "上传列表读取失败"; }
  finally { if (request === sequence) loading.value = false; }
}
async function action(row: UploadRow, kind: UploadAction) {
  const upload = items.value.find(item => item.id === row.id);
  if (!upload || busyId.value) return;
  busyId.value = row.id;
  try {
    await archiveSettingsApi.action(upload, kind);
    notifySuccess(kind === "RETRY" ? "已受理重试上传，将继续使用这次上传的原目标和文件。" : "操作已受理，状态以执行结果为准。");
    await load(false, true);
  } catch (cause) { notifyError(cause); await load(false, true); }
  finally { busyId.value = null; }
}
function schedule() {
  clearTimeout(timer);
  if (!active) return;
  timer = setTimeout(async () => { if (!document.hidden && !loading.value) await load(false, true); schedule(); }, rows.value.some(row => ["pending", "active"].includes(uploadGroup(row.state))) ? 2500 : 12000);
}
function stop() { active = false; clearTimeout(timer); sequence++; loading.value = false; }
onActivated(() => { active = true; void load().then(schedule); });
onDeactivated(stop); onBeforeUnmount(stop);
watch(platform, () => { items.value = []; nextCursor.value = null; loaded.value = false; void load(); });
watch(dataRevision, () => { if (active && !busyId.value) void load(false, true); });
watch(() => route.query.platform, value => { if (route.path === "/archives") platform.value = String(value || ""); }, { immediate: true });
</script>
<template>
  <div>
    <div class="page-header"><div><div class="page-eyebrow">上传队列</div><h1 class="page-title">归档管理</h1><p class="page-subtitle">已完整下载且需要归档的项目，在这里上传至 Google Drive。</p></div><el-button :icon="RefreshCw" :loading="loading" @click="load()">刷新</el-button></div>
    <section class="panel" :aria-busy="loading">
      <div class="panel-heading"><h2>上传记录 <span class="count-badge">{{ total ?? items.length }}</span></h2><el-input v-model="q" clearable aria-label="搜索已加载上传记录" placeholder="搜索已加载的名称" class="search-field"><template #prefix><Search :size="16" /></template></el-input></div>
      <SelectFilter :data="[{ title: '平台', key: 'platform', options: [{value:'',label:'全部'}, {value:'BILIBILI',label:'Bilibili'}, {value:'TELEGRAM',label:'Telegram'}, {value:'QUARK',label:'夸克网盘'}] }, { title:'上传状态', key:'group', options:[{value:'',label:'全部'}, ...uploadGroups] }]" :default-values="{platform,group}" @change="platform = $event.platform; group = $event.group" />
      <div v-if="error" class="archive-error" role="alert"><p>{{ error }}{{ loaded ? '，当前显示上次读取的记录。' : '' }}</p><el-button @click="load()">重新读取</el-button></div>
      <p v-if="loading && !loaded" class="archive-loading" role="status">正在读取上传队列…</p>
      <UploadQueue v-else-if="loaded" :items="visible" :busy-id="busyId" :empty-text="q || group ? '已加载记录中没有匹配项' : '暂无需要上传的完整下载项目'" @action="action" />
      <div v-if="nextCursor" class="archive-pagination"><span>已加载 {{ items.length }}{{ total === null ? '' : ` / ${total}` }} 项</span><el-button :loading="loading" @click="load(true)">加载更多上传记录</el-button></div>
    </section>
  </div>
</template>
<style scoped>
.archive-error, .archive-loading { margin: 16px 24px; }
.archive-error { color: var(--el-color-danger); }
.archive-pagination { display: flex; align-items: center; justify-content: center; gap: 16px; padding: 20px; color: var(--el-text-color-secondary); }
</style>
