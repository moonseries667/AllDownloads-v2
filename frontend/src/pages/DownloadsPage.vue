<script setup lang="ts">
import { ref, computed, onMounted, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { Download, ArrowUpRight, Search, FolderOpen } from "lucide-vue-next";
import { PureTable } from "@pureadmin/table";
import { api, dataRevision, notifyError } from "../api/client";
import { formatDate, platformName } from "../format";
import StatusBadge from "../components/StatusBadge.vue";
import SelectFilter from "../components/SelectFilter.vue";
const route = useRoute(),
  router = useRouter(),
  orders = ref<any[]>([]),
  platform = ref(""),
  state = ref(""),
  q = ref(""),
  detail = ref<any>(null),
  open = ref(false),
  root = ref(""),
  loading = ref(false);
const filtered = computed(() =>
  orders.value.filter(
    (o) =>
      (!platform.value || o.platform === platform.value) &&
      (!state.value || o.state === state.value) &&
      (!q.value || o.title.includes(q.value)) &&
      (!route.query.sourceId || o.sourceId === route.query.sourceId),
  ),
);
const columns = [
  { label: "下载内容", slot: "title", minWidth: 280 },
  { label: "平台", slot: "platform", width: 120 },
  { label: "下载进度", slot: "progress", minWidth: 190 },
  { label: "状态", slot: "state", width: 120 },
  { label: "创建时间", slot: "date", width: 190 },
  { label: "操作", slot: "actions", width: 100 },
];
async function load() {
  loading.value = true;
  try {
    orders.value = (await api.get("/download-orders")).items;
    root.value = (await api.get("/settings")).downloadRoot;
  } catch (e) {
    notifyError(e);
  } finally {
    loading.value = false;
  }
}
async function show(id: string) {
  try {
    detail.value = await api.get("/download-orders/" + id);
    open.value = true;
  } catch (e) {
    notifyError(e);
  }
}
onMounted(load);
watch(dataRevision, load);
watch(
  () => route.query.platform,
  (v) => {
    if (route.path === "/downloads") platform.value = String(v || "");
  },
  { immediate: true },
);
</script>
<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-eyebrow">传输中心</div>
        <h1 class="page-title">下载管理</h1>
        <p class="page-subtitle">
          每一笔订单对应一次明确选择，文件范围在提交时固定。
        </p>
      </div>
      <el-button :icon="ArrowUpRight" @click="router.push('/tasks')"
        >查看执行任务</el-button
      >
    </div>
    <div class="path-banner">
      <FolderOpen :size="21" />
      <div>
        <span>统一本机下载根</span><strong>{{ root }}</strong>
      </div>
      <span class="path-banner-note">所有平台共用 · 仅在设置中修改</span>
    </div>
    <div class="panel">
      <div class="panel-heading">
        <h2>
          下载订单 <span class="count-badge">{{ filtered.length }}</span>
        </h2>
        <el-input
          v-model="q"
          placeholder="搜索下载内容"
          aria-label="搜索下载内容"
          clearable
          class="search-field"
          ><template #prefix><Search :size="16" /></template
        ></el-input>
      </div>
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
          {
            title: '状态',
            key: 'state',
            options: [
              { value: '', label: '全部' },
              { value: 'QUEUED', label: '等待中' },
              { value: 'RUNNING', label: '下载中' },
              { value: 'VERIFIED', label: '已验证' },
              { value: 'NEEDS_ACTION', label: '需要处理' },
            ],
          },
        ]"
        :default-values="{ platform, state }"
        @change="
          platform = $event.platform;
          state = $event.state;
        "
      />
      <PureTable
        :data="filtered"
        :columns="columns"
        :loading="loading"
        row-key="id"
        empty-text="暂无下载订单，请从平台页面选择内容并提交"
        locale="zhCn"
        ><template #title="{ row }"
          ><div class="table-title">
            <Download :size="18" />
            <div>
              <strong>{{ row.title }}</strong
              ><small>{{ row.id }}</small>
            </div>
          </div></template
        ><template #platform="{ row }">{{
          platformName[row.platform]
        }}</template
        ><template #progress="{ row }"
          ><div class="progress-label">
            {{ row.completedCount }} / {{ row.totalCount }} 个文件
          </div>
          <el-progress
            :percentage="
              row.totalCount
                ? Math.round((row.completedCount / row.totalCount) * 100)
                : 0
            "
            :show-text="false"
            :stroke-width="5" /></template
        ><template #state="{ row }"><StatusBadge :state="row.state" /></template
        ><template #date="{ row }"
          ><span class="mono-small">{{
            formatDate(row.createdAt)
          }}</span></template
        ><template #actions="{ row }"
          ><el-button link type="primary" @click="show(row.id)"
            >订单详情</el-button
          ></template
        ></PureTable
      >
    </div>
    <p class="muted">
      Telegram
      收藏与消息链接任务保留原有流程，执行进度也可在「任务与问题」查看。上传进度在「文件与归档」单独展示。
    </p>
    <el-drawer v-model="open" title="下载订单详情" size="570px"
      ><template v-if="detail"
        ><h2>{{ detail.title }}</h2>
        <StatusBadge :state="detail.state" />
        <p class="muted">{{ detail.id }}</p>
        <h3>提交时的文件清单</h3>
        <div v-for="(item, i) in detail.items" :key="i" class="file-detail-row">
          <strong>{{ item.title || item.relativePath }}</strong>
          <p class="path-text">{{ item.relativePath }}</p>
          <StatusBadge :state="item.state || 'QUEUED'" />
        </div>
        <h3>关联执行任务</h3>
        <el-button
          v-for="taskId in detail.taskIds"
          :key="taskId"
          link
          type="primary"
          @click="
            open = false;
            router.push('/tasks?task=' + taskId);
          "
          >{{ taskId }} <ArrowUpRight :size="14" /></el-button
        ><el-alert
          title="受理与百分比不代表已完成；以文件验证状态为准。"
          type="info"
          :closable="false"
          class="mt20" /></template
    ></el-drawer>
  </div>
</template>
