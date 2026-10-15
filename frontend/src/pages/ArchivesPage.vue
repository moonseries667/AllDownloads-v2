<script setup lang="ts">
import { ref, computed, onMounted, watch } from "vue";
import { useRoute } from "vue-router";
import { PureTable } from "@pureadmin/table";
import {
  Archive,
  Search,
  Trash2,
  FolderOpen,
  CloudUpload,
  ShieldCheck,
} from "lucide-vue-next";
import { api, dataRevision, notifyError, notifySuccess } from "../api/client";
import { formatBytes, platformName } from "../format";
import StatusBadge from "../components/StatusBadge.vue";
import SelectFilter from "../components/SelectFilter.vue";
const route = useRoute(),
  items = ref<any[]>([]),
  platform = ref(""),
  q = ref(""),
  state = ref(""),
  open = ref(false),
  detail = ref<any>(null),
  cleanup = ref<any>(null),
  cleanupOpen = ref(false),
  confirmed = ref(false),
  cleaning = ref(false);
const filtered = computed(() =>
  items.value.filter(
    (x) =>
      (!platform.value || x.platform === platform.value) &&
      (!q.value ||
        x.title.includes(q.value) ||
        x.files.some((f: any) => f.relativePath.includes(q.value))) &&
      (!state.value || x.archiveState === state.value),
  ),
);
const columns = [
  { label: "内容 / 本机相对目录", slot: "title", minWidth: 340 },
  { label: "平台", slot: "platform", width: 120 },
  { label: "本机成果", slot: "local", width: 125 },
  { label: "云端归档", slot: "archive", minWidth: 145 },
  { label: "操作", slot: "actions", width: 125 },
];
async function load() {
  try {
    items.value = (await api.get("/artifacts")).items;
    if (detail.value)
      detail.value = items.value.find((x) => x.id === detail.value.id);
  } catch (e) {
    notifyError(e);
  }
}
async function show(id: string) {
  try {
    detail.value = await api.get("/artifacts/" + id);
    open.value = true;
  } catch (e) {
    notifyError(e);
  }
}
async function action(id: string, kind: string) {
  try {
    const result = await api.post("/artifacts/" + id + "/" + kind);
    notifySuccess(
      kind === "open-local"
        ? result.message
        : kind === "archive"
          ? "已加入上传队列，保留本机成果，不重新下载。"
          : "已申请重新核对，结果以执行端确认状态为准。",
    );
  } catch (e) {
    notifyError(e);
  }
}
async function prepareCleanup() {
  try {
    cleanup.value = await api.post("/cleanup-previews");
    confirmed.value = false;
    cleanupOpen.value = true;
  } catch (e) {
    notifyError(e);
  }
}
async function submitCleanup() {
  cleaning.value = true;
  try {
    await api.post("/cleanup-orders", { previewId: cleanup.value.id });
    cleanupOpen.value = false;
    notifySuccess("演示清理状态已更新；未删除任何真实文件。");
  } catch (e) {
    notifyError(e);
  } finally {
    cleaning.value = false;
  }
}
onMounted(load);
watch(dataRevision, load);
watch(
  () => route.query.platform,
  (v) => {
    if (route.path === "/archives") platform.value = String(v || "");
  },
  { immediate: true },
);
</script>
<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-eyebrow">成果与留存</div>
        <h1 class="page-title">文件与归档</h1>
        <p class="page-subtitle">
          本机成果独立保存，云端核对完成后才算归档完成。
        </p>
      </div>
      <el-button :icon="Trash2" @click="prepareCleanup">清理本机副本</el-button>
    </div>
    <div class="archive-stats">
      <div>
        <FolderOpen :size="20" /><span
          >本机已验证<strong>{{
            items.filter((x) => x.localState === "VERIFIED").length
          }}</strong></span
        >
      </div>
      <div>
        <CloudUpload :size="20" /><span
          >待上传 / 核对<strong>{{
            items.filter((x) =>
              ["QUEUED", "UPLOADING", "VERIFYING"].includes(x.archiveState),
            ).length
          }}</strong></span
        >
      </div>
      <div>
        <ShieldCheck :size="20" /><span
          >云端已验证<strong>{{
            items.filter((x) => x.archiveState === "VERIFIED").length
          }}</strong></span
        >
      </div>
    </div>
    <div class="panel">
      <div class="panel-heading">
        <h2>
          全部成果 <span class="count-badge">{{ filtered.length }}</span>
        </h2>
        <el-input
          v-model="q"
          clearable
          aria-label="搜索归档"
          placeholder="搜索标题或文件名"
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
            title: '云端状态',
            key: 'state',
            options: [
              { value: '', label: '全部' },
              { value: 'NOT_REQUESTED', label: '尚未归档' },
              { value: 'VERIFYING', label: '待核对' },
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
      /><PureTable
        :data="filtered"
        :columns="columns"
        row-key="id"
        locale="zhCn"
        empty-text="暂无匹配成果"
        ><template #title="{ row }"
          ><div class="table-title">
            <Archive :size="19" />
            <div>
              <button class="table-link" @click="show(row.id)">
                {{ row.title }}</button
              ><small class="path-text">{{ row.relativeDirectory }}</small>
            </div>
          </div></template
        ><template #platform="{ row }">{{
          platformName[row.platform]
        }}</template
        ><template #local="{ row }"
          ><StatusBadge :state="row.localState" /></template
        ><template #archive="{ row }"
          ><StatusBadge
            :state="row.archiveState"
            :label="
              row.archiveState === 'NOT_REQUESTED' ? '尚未归档' : undefined
            " /></template
        ><template #actions="{ row }"
          ><el-button link type="primary" @click="show(row.id)"
            >成果详情</el-button
          ></template
        ></PureTable
      >
    </div>
    <el-drawer v-model="open" title="成果与归档详情" size="600px"
      ><template v-if="detail"
        ><h2>{{ detail.title }}</h2>
        <dl class="detail-facts">
          <dt>本机状态</dt>
          <dd><StatusBadge :state="detail.localState" /></dd>
          <dt>云端状态</dt>
          <dd>
            <StatusBadge
              :state="detail.archiveState"
              :label="
                detail.archiveState === 'NOT_REQUESTED' ? '尚未归档' : undefined
              "
            />
          </dd>
          <dt>本机相对目录</dt>
          <dd class="break-anywhere">{{ detail.relativeDirectory }}</dd>
          <dt>云端位置</dt>
          <dd>{{ detail.archivePath || "尚未形成云端成果" }}</dd>
        </dl>
        <div class="toolbar">
          <el-button
            :icon="FolderOpen"
            :disabled="!['VERIFIED', 'PARTIAL'].includes(detail.localState)"
            @click="action(detail.id, 'open-local')"
            >打开本机文件夹</el-button
          ><a
            v-if="detail.cloudUrl"
            :href="detail.cloudUrl"
            target="_blank"
            rel="noopener"
            >打开云端文件</a
          ><el-button v-else disabled>云端链接不可用</el-button>
        </div>
        <h3>文件与必要元数据</h3>
        <el-table :data="detail.files"
          ><el-table-column
            prop="relativePath"
            label="文件"
            min-width="220" /><el-table-column label="大小" width="100"
            ><template #default="{ row }">{{
              formatBytes(row.sizeBytes)
            }}</template></el-table-column
          ><el-table-column label="核对" width="100"
            ><template #default="{ row }"
              ><StatusBadge :state="row.state" /></template></el-table-column
        ></el-table>
        <div class="mt20">
          <el-alert
            v-for="warning in detail.warnings"
            :key="warning"
            :title="warning"
            type="warning"
            :closable="false"
            show-icon
          />
        </div>
        <div class="toolbar mt20">
          <el-button
            type="primary"
            :icon="CloudUpload"
            :disabled="detail.localState !== 'VERIFIED'"
            @click="action(detail.id, 'archive')"
            >{{
              detail.archiveState === "NEEDS_ACTION"
                ? "重试上传"
                : "加入归档队列"
            }}</el-button
          ><el-button :icon="ShieldCheck" @click="action(detail.id, 'verify')"
            >重新核对</el-button
          >
        </div>
        <p class="muted">
          重试上传仅处理本机已完成成果。必要清单缺失会阻止归档；补充项失败会保留警告。
        </p></template
      ></el-drawer
    >
    <el-dialog v-model="cleanupOpen" title="预览本机副本清理" width="690px"
      ><template v-if="cleanup"
        ><el-alert
          title="仅包含云端已验证完整的本应用成品；不会清理旧版数据、人工文件或云端对象。"
          type="warning"
          :closable="false"
          show-icon
        />
        <div class="preview-summary">
          <span
            ><b>{{ cleanup.items.length }}</b> 项可清理</span
          ><span>预计释放 {{ formatBytes(cleanup.bytes) }}</span>
        </div>
        <div
          v-for="item in cleanup.items"
          :key="item.id"
          class="file-detail-row"
        >
          <strong>{{ item.title }}</strong>
          <p class="path-text">{{ item.relativeDirectory }}</p>
        </div>
        <el-empty
          v-if="!cleanup.items.length"
          description="没有符合条件的本机副本"
          :image-size="65"
        /><el-checkbox v-model="confirmed" :disabled="!cleanup.items.length"
          >我已核对清单，确认清理这些本机副本</el-checkbox
        ></template
      ><template #footer
        ><el-button @click="cleanupOpen = false">保留本机副本</el-button
        ><el-button
          type="danger"
          :disabled="!confirmed || !cleanup?.items.length"
          :loading="cleaning"
          @click="submitCleanup"
          >确认清理</el-button
        ></template
      ></el-dialog
    >
  </div>
</template>
