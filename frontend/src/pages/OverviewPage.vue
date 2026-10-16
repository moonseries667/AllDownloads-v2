<script setup lang="ts">
import { computed, ref, onMounted, watch } from "vue";
import { useRouter } from "vue-router";
import {
  MonitorPlay,
  Send,
  CloudDownload as FolderCloud,
  ArrowUpRight,
  ArrowRight,
  HardDrive,
  CheckCheck,
  Activity,
  Library,
  AlertCircle,
  CloudUpload,
} from "lucide-vue-next";
import { api, dataRevision, isDemo } from "../api/client";
import { formatDate, platformName, taskPhaseName } from "../format";
import StatusBadge from "../components/StatusBadge.vue";
import RuntimeSummary from "../components/RuntimeSummary.vue";
import { readAllPages } from "../api/pages";
import { readAttentionIssues } from "../api/notifications";
import type { ServerIssue } from "../notifications/store";
const router = useRouter(),
  stats = ref<any>({}),
  tasks = ref<any[]>([]),
  issues = ref<ServerIssue[]>([]),
  settings = ref<any>({});
const loading = ref(true), error = ref("");
const taskError = ref(""), issueError = ref(""), storageError = ref("");
const activeTasks = computed(() => tasks.value.filter(task => ["QUEUED", "RUNNING", "PAUSING", "WAITING_RETRY"].includes(task.status)));
async function load() {
    const results = await Promise.allSettled([
        api.get("/overview"),
        readAllPages<any>("/tasks"),
        readAttentionIssues(),
        api.get("/settings"),
      ]);
    if (results[0].status === "fulfilled") { stats.value = results[0].value; error.value = ""; }
    else error.value = results[0].reason.message;
    if (results[1].status === "fulfilled") { tasks.value = results[1].value; taskError.value = ""; }
    else taskError.value = results[1].reason.message;
    if (results[2].status === "fulfilled") { issues.value = results[2].value.filter(issue => !issue.resolved); issueError.value = ""; }
    else issueError.value = results[2].reason.message;
    if (results[3].status === "fulfilled") { settings.value = results[3].value; storageError.value = ""; }
    else storageError.value = results[3].reason.message;
    loading.value = false;
}
onMounted(load);
watch(dataRevision, load);
const platforms = [
  {
    name: "Bilibili",
    eyebrow: "视频与系列",
    description: "收藏夹、合集与番剧，按需保存。",
    icon: MonitorPlay,
    path: "/bilibili",
    color: "pink",
    action: "浏览内容",
  },
  {
    name: "Telegram",
    eyebrow: "消息与附件",
    description: "收藏增量与完整媒体组。",
    icon: Send,
    path: "/telegram",
    color: "blue",
    action: "打开收藏",
  },
  {
    name: "夸克网盘",
    eyebrow: "分享与文件",
    description: "持续转存更新，再选择文件下载。",
    icon: FolderCloud,
    path: "/quark/cloud",
    color: "purple",
    action: "进入云盘",
  },
];
</script>
<template>
  <div>
    <RuntimeSummary :runtime="stats.runtime" :loading="loading" :error="error" :demo="isDemo" />
    <div class="overview-stats">
      <div
        v-for="item in [
          { icon: Library, value: stats.bilibili?.downloadCount, label: 'Bilibili 已下载' },
          { icon: Activity, value: taskError ? null : activeTasks.length, label: '活动任务' },
          { icon: CheckCheck, value: stats.bilibili?.archiveCount, label: 'Bilibili 已归档' },
          { icon: AlertCircle, value: issueError ? null : issues.length, label: '需要处理' },
        ]"
        :key="item.label"
      >
        <span class="stat-icon"><component :is="item.icon" :size="20" /></span>
        <div>
          <span>{{ item.label }}</span
          ><strong>{{ item.value ?? "—" }}</strong>
        </div>
      </div>
    </div>
    <div class="section-heading">
      <h2>内容来源</h2>
      <span class="muted">三个入口，一处管理</span>
    </div>
    <div class="platform-grid">
      <router-link
        v-for="p in platforms"
        :key="p.name"
        :to="p.path"
        class="platform-card"
        :class="p.color"
        ><div class="platform-card-top">
          <span class="platform-icon"
            ><component :is="p.icon" :size="28" /></span
          ><ArrowUpRight :size="20" />
        </div>
        <span class="platform-eyebrow">{{ p.eyebrow }}</span>
        <h2>{{ p.name }}</h2>
        <p>{{ p.description }}</p>
        <div class="platform-card-bottom">
          {{ p.action }}<ArrowRight :size="17" /></div
      ></router-link>
    </div>
    <div class="overview-columns">
      <section class="panel">
        <div class="panel-heading">
          <h2>正在进行</h2>
          <router-link to="/tasks"
            >全部任务 <ArrowUpRight :size="14"
          /></router-link>
        </div>
        <p v-if="taskError" class="text-danger" role="alert">任务读取失败：{{ taskError }}</p>
        <div v-else-if="!activeTasks.length" class="empty-state">
          暂无任务，选择内容后即可开始。
        </div>
        <button
          v-for="task in activeTasks.slice(0, 4)"
          :key="task.id"
          class="activity-row"
          @click="router.push('/tasks')"
        >
          <span class="activity-icon"
            ><component
              :is="
                task.kind === 'UPLOAD'
                  ? CloudUpload
                  : task.kind === 'VERIFY'
                    ? CheckCheck
                    : Activity
              "
              :size="19"
          /></span>
          <div class="activity-info">
            <strong>{{ task.title || taskPhaseName(task.phase) }}</strong
            ><span>{{ platformName[task.platform] }} · {{ taskPhaseName(task.phase) }}</span>
          </div>
          <StatusBadge :state="task.status" />
        </button>
      </section>
      <section class="panel attention-panel">
        <div class="panel-heading">
          <h2>
            需要关注 <span class="count-badge">{{ issues.length }}</span>
          </h2>
        </div>
        <p v-if="issueError" class="text-danger" role="alert">问题读取失败：{{ issueError }}</p>
        <div v-else-if="!issues.length" class="empty-state">当前没有待处理问题</div>
        <div
          v-for="issue in issues.slice(0, 2)"
          :key="issue.id"
          class="attention-item"
        >
          <div>
            <AlertCircle :size="17" /><strong>{{ issue.severity === 'WARNING' ? '任务需要关注' : '任务未完成' }}</strong>
          </div>
          <p>{{ issue.message }}</p>
          <router-link :to="issue.taskId ? '/tasks?taskId=' + encodeURIComponent(issue.taskId) : '/tasks'"
            >查看处理方式 <ArrowRight :size="14"
          /></router-link>
        </div>
      </section>
    </div>
    <section class="storage-strip">
      <span class="storage-icon"><HardDrive :size="24" /></span>
      <div>
        <strong>统一下载目录</strong>
        <p>{{ storageError ? '存储摘要读取失败：' + storageError : settings.downloadRoot || '尚未配置下载目录' }}</p>
      </div>
      <div class="storage-cloud">
        <span>云端归档目标</span
        ><strong>{{ storageError ? '状态未知' : settings.archiveDestinationLabel || '尚未配置' }}</strong>
      </div>
      <router-link to="/settings"
        >管理存储 <ArrowUpRight :size="15"
      /></router-link>
    </section>
    <p v-if="isDemo" class="muted overview-footnote">
      演示数据包含未下载、部分完成和上传待处理情况。所有真实文件结果将在后续接入
      Java 后核实。
    </p>
  </div>
</template>
