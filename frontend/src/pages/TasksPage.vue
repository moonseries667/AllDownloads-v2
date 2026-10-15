<script setup lang="ts">
import { ref, computed, onMounted, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { PureTable } from "@pureadmin/table";
import { RefreshCw, AlertCircle, Activity } from "lucide-vue-next";
import { api, dataRevision, notifyError, notifySuccess } from "../api/client";
import { formatDate, formatBytes, platformName } from "../format";
import StatusBadge from "../components/StatusBadge.vue";
import SelectFilter from "../components/SelectFilter.vue";
const route = useRoute(),
  router = useRouter(),
  tasks = ref<any[]>([]),
  issues = ref<any[]>([]),
  platform = ref(""),
  status = ref(""),
  stage = ref(""),
  tab = ref("tasks"),
  detail = ref<any>(null),
  open = ref(false),
  busy = ref("");
const actionName: Record<string, string> = {
  PAUSE: "暂停",
  RESUME: "继续",
  CANCEL: "取消",
  RETRY: "重试此阶段",
  RECONCILE: "核对结果",
};
const kindName: Record<string, string> = {
  INFO_SYNC: "信息收录",
  PREVIEW: "下载预览",
  TRANSFER_SYNC: "网盘转存",
  DOWNLOAD: "媒体下载",
  UPLOAD: "云端上传",
  VERIFY: "成果核对",
  AUTH: "账号登录",
};
const filtered = computed(() =>
  tasks.value.filter(
    (t) =>
      (!platform.value || t.platform === platform.value) &&
      (!status.value || t.status === status.value) &&
      (!stage.value || t.kind === stage.value),
  ),
);
const columns = [
  { label: "任务 / 当前阶段", slot: "title", minWidth: 270 },
  { label: "平台", slot: "platform", width: 105 },
  { label: "状态", slot: "state", width: 150 },
  { label: "已确认进度", slot: "progress", minWidth: 180 },
  { label: "可用操作", slot: "actions", minWidth: 220 },
];
async function load() {
  try {
    tasks.value = (await api.get("/tasks")).items;
    issues.value = (await api.get("/issues")).items;
    if (detail.value)
      detail.value = tasks.value.find((t) => t.id === detail.value.id);
  } catch (e) {
    notifyError(e);
  }
}
async function act(t: any, action: string) {
  busy.value = t.id + action;
  try {
    await api.post("/tasks/" + t.id + "/actions", { action }, t.revision);
    notifySuccess(
      action === "CANCEL"
        ? "取消请求已记录，等待执行端确认"
        : action === "PAUSE"
          ? "暂停请求已记录，等待执行端确认"
          : "已重新排队，仅处理当前阶段",
    );
  } catch (e) {
    notifyError(e);
  } finally {
    busy.value = "";
  }
}
async function acknowledge(id: string) {
  try {
    await api.post("/issues/" + id + "/acknowledge");
    notifySuccess("已标为已读，问题仍需实际处理");
  } catch (e) {
    notifyError(e);
  }
}
async function show(t: any) {
  try {
    detail.value = await api.get("/tasks/" + t.id);
    open.value = true;
  } catch (e) {
    notifyError(e);
  }
}
onMounted(async () => {
  await load();
  if (route.query.task) {
    const t = tasks.value.find((t) => t.id === route.query.task);
    if (t) show(t);
  }
});
watch(dataRevision, load);
</script>
<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-eyebrow">执行与诊断</div>
        <h1 class="page-title">任务与问题</h1>
        <p class="page-subtitle">按阶段追踪进展，保留已经完成的部分。</p>
      </div>
      <el-button :icon="RefreshCw" @click="load">刷新状态</el-button>
    </div>
    <el-tabs v-model="tab"
      ><el-tab-pane
        :label="'全部任务 · ' + tasks.length"
        name="tasks" /><el-tab-pane
        :label="'需要处理 · ' + issues.length"
        name="issues"
    /></el-tabs>
    <div v-if="tab === 'tasks'" class="panel">
      <div class="task-filter">
        <SelectFilter
          :data="[
            {
              title: '平台',
              key: 'platform',
              options: [
                { value: '', label: '全部' },
                { value: 'BILIBILI', label: 'Bilibili' },
                { value: 'TELEGRAM', label: 'Telegram' },
                { value: 'QUARK', label: '夸克网盘' },
              ],
            },
          ]"
          :default-values="{ platform }"
          @change="platform = $event.platform"
        />
        <div class="toolbar">
          <el-select
            v-model="status"
            aria-label="任务状态"
            style="width: 155px"
            clearable
            placeholder="全部状态"
            ><el-option
              v-for="s in [
                'QUEUED',
                'RUNNING',
                'PAUSING',
                'PAUSED',
                'NEEDS_ACTION',
                'FAILED',
                'SUCCEEDED',
              ]"
              :key="s"
              :value="s"
              :label="
                (
                  {
                    QUEUED: '等待中',
                    RUNNING: '进行中',
                    PAUSING: '正在暂停',
                    PAUSED: '已暂停',
                    NEEDS_ACTION: '需要处理',
                    FAILED: '失败',
                    SUCCEEDED: '已完成',
                  } as any
                )[s]
              " /></el-select
          ><el-select
            v-model="stage"
            aria-label="任务阶段"
            style="width: 155px"
            clearable
            placeholder="全部阶段"
            ><el-option
              v-for="(v, k) in kindName"
              :key="k"
              :value="k"
              :label="v"
          /></el-select>
        </div>
      </div>
      <PureTable
        :data="filtered"
        :columns="columns"
        row-key="id"
        locale="zhCn"
        empty-text="暂无匹配任务"
        ><template #title="{ row }"
          ><button class="table-link" @click="show(row)">
            {{ row.title }}
          </button>
          <div class="muted">
            {{ kindName[row.kind] }} · {{ row.phase }}
          </div></template
        ><template #platform="{ row }">{{
          platformName[row.platform]
        }}</template
        ><template #state="{ row }"
          ><StatusBadge
            :state="row.status"
            :label="
              row.desiredAction === 'CANCEL' ? '正在取消' : undefined
            " /></template
        ><template #progress="{ row }"
          ><span v-if="row.progress.totalItems !== null"
            >{{ row.progress.doneItems }} /
            {{ row.progress.totalItems }} 项</span
          ><span v-else class="muted">总量待确认</span>
          <div class="muted" v-if="row.progress.bytesPerSecond">
            {{ formatBytes(row.progress.bytesPerSecond) }}/s
          </div></template
        ><template #actions="{ row }"
          ><el-button
            link
            type="primary"
            v-for="action in row.allowedActions"
            :key="action"
            :loading="busy === row.id + action"
            @click="act(row, action)"
            >{{ actionName[action] }}</el-button
          ><span v-if="!row.allowedActions.length" class="muted"
            >等待确认</span
          ></template
        ></PureTable
      >
    </div>
    <div v-else class="issue-list">
      <el-empty v-if="!issues.length" description="没有需要处理的问题" />
      <article v-for="issue in issues" :key="issue.id" class="panel issue-card">
        <div class="issue-card-title">
          <AlertCircle :size="22" />
          <div>
            <h2>{{ issue.title }}</h2>
            <span class="muted"
              >{{ platformName[issue.platform] }} · {{ issue.phase }}</span
            >
          </div>
          <span
            class="status-badge"
            :class="issue.acknowledged ? 'muted' : 'bad'"
            >{{ issue.acknowledged ? "已读 · 未解决" : "待处理" }}</span
          >
        </div>
        <p>{{ issue.error.message }}</p>
        <div class="next-step">
          <strong>下一步</strong
          >{{ issue.error.action || "查看任务详情，处理原因后重试当前阶段。" }}
        </div>
        <div class="toolbar">
          <el-button
            type="primary"
            plain
            @click="show(tasks.find((t) => t.id === issue.id))"
            >任务详情</el-button
          ><el-button
            v-if="issue.error.code === 'AUTH_REQUIRED'"
            @click="router.push('/settings')"
            >前往账号设置</el-button
          ><el-button
            :disabled="issue.acknowledged"
            @click="acknowledge(issue.id)"
            >标为已读</el-button
          >
        </div>
      </article>
    </div>
    <el-drawer v-model="open" title="任务详情" size="540px"
      ><template v-if="detail"
        ><h2>{{ detail.title }}</h2>
        <StatusBadge
          :state="detail.status"
          :label="detail.desiredAction === 'CANCEL' ? '正在取消' : undefined"
        />
        <dl class="detail-facts">
          <dt>任务 ID</dt>
          <dd>{{ detail.id }}</dd>
          <dt>当前阶段</dt>
          <dd>{{ detail.phase }}</dd>
          <dt>已完成字节</dt>
          <dd>{{ formatBytes(detail.progress.doneBytes) }}</dd>
          <dt>已确认总量</dt>
          <dd>{{ formatBytes(detail.progress.totalBytes) }}</dd>
          <dt>创建时间</dt>
          <dd>{{ formatDate(detail.createdAt) }}</dd>
          <dt>下次重试</dt>
          <dd>{{ formatDate(detail.nextRetryAt) }}</dd>
        </dl>
        <h3>阶段记录</h3>
        <el-timeline
          ><el-timeline-item
            v-for="step in detail.completedSteps"
            :key="step"
            type="success"
            >{{ step }}</el-timeline-item
          ><el-timeline-item type="primary">{{ detail.phase }}</el-timeline-item
          ><el-timeline-item
            v-for="step in detail.remainingSteps"
            :key="step"
            >{{ step }}</el-timeline-item
          ></el-timeline
        ><el-alert
          v-if="detail.error"
          :title="detail.error.message"
          :description="detail.error.action"
          type="error"
          :closable="false"
          show-icon
        />
        <div class="toolbar mt20">
          <el-button
            v-for="action in detail.allowedActions"
            :key="action"
            @click="act(detail, action)"
            :loading="busy === detail.id + action"
            >{{ actionName[action] }}</el-button
          >
        </div>
        <p class="muted">
          控制请求先记录意图，最终结果由执行端确认。仅重试失败阶段，不重复之前的下载或转存。
        </p></template
      ></el-drawer
    >
  </div>
</template>
