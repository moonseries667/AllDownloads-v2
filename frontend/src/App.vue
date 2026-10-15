<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import {
  LayoutDashboard,
  MonitorPlay,
  Send,
  CloudDownload as FolderCloud,
  Download,
  Archive,
  Activity,
  Settings,
  Search,
  ArrowDownToLine,
  Bell,
  ChevronRight,
  PanelLeftClose,
  PanelLeftOpen,
  ArrowUpRight,
} from "lucide-vue-next";
import AppMain from "./components/AppMain.vue";
import { api, dataRevision, isDemo, notifyError } from "./api/client";
import { platformName } from "./format";
const route = useRoute(),
  router = useRouter(),
  collapsed = ref(false),
  searchOpen = ref(false),
  q = ref(""),
  results = ref<any[]>([]),
  searching = ref(false),
  issues = ref<any[]>([]),
  overview = ref<any>({}),
  scenario = ref("normal"),
  connectionError = ref("");
const nav = [
  { label: "总览", path: "/", icon: LayoutDashboard },
  { label: "Bilibili", path: "/bilibili", icon: MonitorPlay },
  { label: "Telegram", path: "/telegram", icon: Send },
  { label: "夸克网盘", path: "/quark/cloud", icon: FolderCloud },
  { label: "下载管理", path: "/downloads", icon: Download },
  { label: "文件与归档", path: "/archives", icon: Archive },
  { label: "任务与问题", path: "/tasks", icon: Activity },
];
let searchRun = 0,
  timer: ReturnType<typeof setTimeout> | undefined,
  searchTimer: ReturnType<typeof setTimeout> | undefined;
async function load() {
  try {
    [overview.value, issues.value] = await Promise.all([
      api.get("/overview"),
      api.get("/issues").then((x) => x.items),
    ]);
    if (isDemo) scenario.value = (await api.get("/__demo/status")).scenario;
    connectionError.value = "";
  } catch (e) {
    connectionError.value = (e as Error).message;
  }
}
async function search() {
  const n = ++searchRun;
  searching.value = true;
  try {
    const d = await api.get("/search?q=" + encodeURIComponent(q.value));
    if (n === searchRun) results.value = d.items;
  } catch (e) {
    notifyError(e);
  } finally {
    if (n === searchRun) searching.value = false;
  }
}
watch(q, () => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(search, 180);
});
watch(dataRevision, load);
watch(
  () => route.path,
  () => {
    document.title = route.meta.title + " · AllDownloads";
  },
);
async function poll() {
  if (!isDemo) {
    await load();
    dataRevision.value++;
  }
  timer = setTimeout(
    poll,
    document.hidden ? 15000 : overview.value.activeTasks ? 2000 : 10000,
  );
}
function keys(e: KeyboardEvent) {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
    e.preventDefault();
    searchOpen.value = true;
  }
}
onMounted(() => {
  load();
  poll();
  document.addEventListener("keydown", keys);
});
onBeforeUnmount(() => {
  clearTimeout(timer);
  clearTimeout(searchTimer);
  document.removeEventListener("keydown", keys);
});
</script>
<template>
  <div class="app-shell" :class="{ collapsed }">
    <a class="skip-link" href="#main-content">跳到主要内容</a>
    <aside class="sidebar">
      <router-link to="/" class="brand"
        ><span class="brand-symbol"><ArrowDownToLine :size="23" /></span
        ><span class="brand-text"
          >AllDownloads<span>内容，由你掌握</span></span
        ></router-link
      >
      <span class="nav-caption" v-if="!collapsed">工作空间</span>
      <nav aria-label="主导航">
        <template v-for="(item, index) in nav" :key="item.path"
          ><div v-if="index === 4" class="nav-divider"></div>
          <router-link
            :to="item.path"
            :title="item.label"
            :class="{ active: route.path === item.path }"
            ><component :is="item.icon" :size="19" /><span>{{
              item.label
            }}</span
            ><b v-if="item.path === '/tasks' && overview.issues">{{
              overview.issues
            }}</b></router-link
          ></template
        >
      </nav>
      <div class="sidebar-bottom">
        <div class="workspace-label" v-if="!collapsed">
          <span class="workspace-avatar">A</span>
          <div>
            个人内容空间<small>{{
              isDemo ? "隔离演示数据" : "业务工作台"
            }}</small>
          </div>
        </div>
        <router-link
          to="/settings"
          :class="{ active: route.path === '/settings' }"
          title="设置"
          ><Settings :size="19" /><span>设置与账号</span></router-link
        >
      </div>
    </aside>
    <div class="main-column">
      <header class="topbar">
        <button
          class="icon-button"
          :aria-label="collapsed ? '展开导航' : '收起导航'"
          @click="collapsed = !collapsed"
        >
          <component
            :is="collapsed ? PanelLeftOpen : PanelLeftClose"
            :size="18"
          />
        </button>
        <div class="breadcrumb">
          <span>工作空间</span><ChevronRight :size="14" /><strong>{{
            route.meta.title
          }}</strong>
        </div>
        <button class="global-search" @click="searchOpen = true">
          <Search :size="17" /><span>搜索标题、文件名…</span><kbd>Ctrl K</kbd>
        </button>
        <span v-if="isDemo" class="demo-indicator">前端演示</span>
        <el-popover placement="bottom-end" width="360" trigger="click"
          ><template #reference
            ><button
              class="icon-button notification-button"
              aria-label="查看问题通知"
            >
              <Bell :size="19" /><span
                v-if="issues.length"
                class="notification-dot"
              ></span></button
          ></template>
          <h3>需要关注</h3>
          <p v-if="!issues.length" class="muted">当前没有待处理问题</p>
          <button
            v-for="issue in issues"
            :key="issue.id"
            class="issue-notice"
            @click="router.push('/tasks')"
          >
            <strong>{{ issue.title }}</strong
            ><span>{{ issue.error.message }}</span>
          </button></el-popover
        >
        <span class="avatar">我</span>
      </header>
      <div
        v-if="scenario === 'offline' || connectionError"
        class="connection-banner"
        role="alert"
      >
        连接已中断 · 当前内容来自缓存，任务状态可能过期。恢复连接后将重新查询。
      </div>
      <AppMain />
      <footer class="app-footer">
        <span
          >{{
            isDemo
              ? "本机前端演示 · 数据与操作均为模拟，不访问真实账号"
              : "AllDownloads"
          }} </span
        ><router-link to="/settings"
          >{{ isDemo ? "切换验收场景" : "运行设置" }}<ArrowUpRight :size="12"
        /></router-link>
      </footer>
    </div>
    <el-dialog
      v-model="searchOpen"
      title="搜索全部内容"
      width="660px"
      destroy-on-close
    >
      <el-input
        v-model="q"
        aria-label="搜索全部内容"
        placeholder="输入标题或文件名，例如：设计"
        clearable
        autofocus
        @keyup.enter="search"
        ><template #prefix><Search :size="18" /></template
      ></el-input>
      <div v-loading="searching" class="search-results">
        <button
          v-for="result in results"
          :key="result.platform + result.id"
          @click="
            router.push(result.url);
            searchOpen = false;
          "
        >
          <span class="platform-chip">{{ platformName[result.platform] }}</span
          ><strong>{{ result.title }}</strong
          ><ChevronRight :size="16" /></button
        ><el-empty
          v-if="!results.length"
          :description="
            q ? '没有匹配的内容' : '搜索 B 站、Telegram 与夸克中的已收录内容'
          "
          :image-size="70"
        />
      </div>
    </el-dialog>
  </div>
</template>
