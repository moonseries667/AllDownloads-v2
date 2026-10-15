<script setup lang="ts">
import { ref, computed, watch, onMounted, onActivated } from "vue";
import { useRoute, useRouter } from "vue-router";
import {
  Plus,
  RefreshCw,
  Download,
  Info,
  ExternalLink,
  FolderHeart,
  Library,
  Clapperboard,
  Layers,
  Search,
  LayoutGrid,
  List,
  Settings2,
  Pin,
  Check,
  ArrowUpRight,
} from "lucide-vue-next";
import { ElMessageBox } from "element-plus";
import { api, dataRevision, notifyError, notifySuccess } from "../api/client";
import type { RecordData, Selection } from "../api/types";
import { formatDate, duration } from "../format";
import StatusBadge from "../components/StatusBadge.vue";
import SelectFilter from "../components/SelectFilter.vue";
import DownloadPreview from "../components/DownloadPreview.vue";
const route = useRoute(),
  router = useRouter();
const contents = ref<RecordData[]>([]),
  sources = ref<RecordData[]>([]),
  source = ref(""),
  q = ref(""),
  type = ref(""),
  state = ref(""),
  sort = ref("original"),
  dateFrom = ref(""),
  dateTo = ref(""),
  page = ref(1),
  view = ref("grid"),
  loading = ref(false),
  error = ref("");
const selectedIds = ref<string[]>([]),
  querySelection = ref(false),
  frozenFilters = ref<Record<string, string>>({}),
  frozenIds = ref<string[]>([]),
  exclude = ref<string[]>([]);
const addOpen = ref(false),
  links = ref(""),
  adding = ref(false),
  addResult = ref<RecordData[]>([]);
const detail = ref<RecordData | null>(null),
  parts = ref<RecordData[]>([]),
  partIds = ref<string[]>([]),
  detailOpen = ref(false),
  sourceOpen = ref(false),
  alias = ref(""),
  pinned = ref(false),
  editingSource = ref<RecordData | null>(null);
const previewOpen = ref(false),
  previewSelection = ref<Selection>({
    mode: "EXPLICIT",
    resource: "BILIBILI_CONTENTS",
    ids: [],
  });
const filters = computed(() => ({
  sourceId: source.value,
  q: q.value,
  type: type.value,
  state: state.value,
  from: dateFrom.value,
  to: dateTo.value,
}));
const filtered = computed(() => {
  const rows = contents.value.filter(
    (c) =>
      (!source.value || c.sourceIds.includes(source.value)) &&
      (!q.value || c.title.toLowerCase().includes(q.value.toLowerCase())) &&
      (!type.value || c.type === type.value) &&
      (!state.value || c.downloadSummary.state === state.value) &&
      (!dateFrom.value || c.publishedAt >= dateFrom.value) &&
      (!dateTo.value || c.publishedAt.slice(0, 10) <= dateTo.value),
  );
  return sort.value === "published"
    ? [...rows].sort((a, b) => b.publishedAt.localeCompare(a.publishedAt))
    : rows;
});
const visible = computed(() =>
  filtered.value.slice((page.value - 1) * 6, page.value * 6),
);
const currentSource = computed(() =>
  sources.value.find((s) => s.id === source.value),
);
const selectionCount = computed(() =>
  querySelection.value
    ? frozenIds.value.length - exclude.value.length
    : selectedIds.value.length,
);
const filterData = [
  {
    title: "内容类型",
    key: "type",
    options: [
      { value: "", label: "全部" },
      { value: "VIDEO", label: "单视频" },
      { value: "MULTIPART", label: "多 P 视频" },
      { value: "SERIES", label: "番剧" },
      { value: "MOVIE", label: "电影" },
    ],
  },
  {
    title: "下载状态",
    key: "state",
    options: [
      { value: "", label: "全部" },
      { value: "NOT_REQUESTED", label: "未下载" },
      { value: "DOWNLOADING", label: "下载中" },
      { value: "PARTIAL", label: "部分完成" },
      { value: "VERIFIED", label: "已完成" },
    ],
  },
];
const kindLabel: Record<string, string> = {
  FAVORITE: "收藏夹",
  COLLECTION: "合集",
  SERIES: "番剧",
  USER_ADDED: "用户添加",
  MOVIE: "电影",
};
async function load() {
  loading.value = true;
  try {
    const [c, s] = await Promise.all([
      api.get("/bilibili/contents"),
      api.get("/bilibili/sources"),
    ]);
    contents.value = c.items;
    sources.value = s.items;
    error.value = "";
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    loading.value = false;
  }
}
watch(dataRevision, load);
watch([source, q, type, state, dateFrom, dateTo], () => (page.value = 1));
watch(
  () => route.query.q,
  (value) => {
    if (route.path === "/bilibili") q.value = String(value || "");
  },
  { immediate: true },
);
onMounted(load);
let refreshed = false;
onActivated(async () => {
  if (refreshed) return;
  refreshed = true;
  try {
    const s = await api.get("/bilibili/sources");
    if (s.items.length)
      await api.post("/bilibili/sources/" + s.items[0].id + "/refresh", {
        force: false,
      });
  } catch (e) {
    error.value = (e as Error).message;
  }
});
function isSelected(id: string) {
  return querySelection.value
    ? frozenIds.value.includes(id) && !exclude.value.includes(id)
    : selectedIds.value.includes(id);
}
function toggle(id: string) {
  if (querySelection.value) {
    if (!frozenIds.value.includes(id)) return;
    exclude.value = isSelected(id)
      ? [...exclude.value, id]
      : exclude.value.filter((x) => x !== id);
  } else
    selectedIds.value = isSelected(id)
      ? selectedIds.value.filter((x) => x !== id)
      : [...selectedIds.value, id];
}
function clear() {
  selectedIds.value = [];
  querySelection.value = false;
  frozenIds.value = [];
  exclude.value = [];
}
function selectAll() {
  querySelection.value = true;
  frozenFilters.value = { ...filters.value };
  frozenIds.value = filtered.value.map((c) => c.id);
  exclude.value = [];
  selectedIds.value = [];
}
function openPreview(id?: string) {
  previewSelection.value = id
    ? { mode: "EXPLICIT", resource: "BILIBILI_CONTENTS", ids: [id] }
    : querySelection.value
      ? {
          mode: "QUERY",
          resource: "BILIBILI_CONTENTS",
          filters: { ...frozenFilters.value },
          excludeIds: [...exclude.value],
        }
      : {
          mode: "EXPLICIT",
          resource: "BILIBILI_CONTENTS",
          ids: [...selectedIds.value],
        };
  previewOpen.value = true;
}
async function showDetail(c: RecordData) {
  detail.value = c;
  detailOpen.value = true;
  try {
    parts.value = (
      await api.get("/bilibili/contents/" + c.id + "/parts")
    ).items;
    partIds.value = parts.value.map((p) => p.id);
  } catch (e) {
    notifyError(e);
  }
}
async function addSources() {
  adding.value = true;
  try {
    const input = links.value.split(/\s+/).filter(Boolean);
    if (!input.length) throw new Error("请输入至少一条来源链接");
    addResult.value = (
      await api.post("/bilibili/sources", { links: input })
    ).items;
    notifySuccess("来源解析请求已受理，仅收录信息。");
  } catch (e) {
    notifyError(e);
  } finally {
    adding.value = false;
  }
}
async function refresh() {
  try {
    const ids = source.value ? [source.value] : sources.value.map((s) => s.id);
    for (const id of ids)
      await api.post("/bilibili/sources/" + id + "/refresh", { force: true });
    notifySuccess("已申请刷新来源信息；没有创建下载订单。");
  } catch (e) {
    notifyError(e);
  }
}
function editSource(s: RecordData) {
  editingSource.value = s;
  alias.value = s.alias;
  pinned.value = s.pinned;
  sourceOpen.value = true;
}
async function saveSource() {
  try {
    await api.patch(
      "/bilibili/sources/" + editingSource.value!.id,
      { alias: alias.value, pinned: pinned.value },
      editingSource.value!.revision,
    );
    sourceOpen.value = false;
    notifySuccess("来源显示设置已更新");
  } catch (e) {
    notifyError(e);
  }
}
async function removeSource() {
  try {
    await ElMessageBox.confirm(
      "仅移除来源关系，保留已下载资料、未结束任务及其他来源引用。",
      "移除来源",
      {
        confirmButtonText: "移除来源",
        cancelButtonText: "取消",
        type: "warning",
      },
    );
    await api.delete(
      "/bilibili/sources/" + editingSource.value!.id,
      editingSource.value!.revision,
    );
    sourceOpen.value = false;
    source.value = "";
    notifySuccess("来源关系已移除，已下载内容保留");
  } catch (e) {
    if (e !== "cancel" && e !== "close") notifyError(e);
  }
}
</script>
<template>
  <div class="bilibili-page">
    <div class="page-header">
      <div>
        <div class="page-eyebrow">内容库 / BILIBILI</div>
        <h1 class="page-title">收藏值得留下的内容</h1>
        <p class="page-subtitle">
          浏览信息，按需下载。每一份内容，都有独立的归档记录。
        </p>
      </div>
      <div class="toolbar">
        <el-button :icon="RefreshCw" @click="refresh">刷新信息</el-button
        ><el-button
          type="primary"
          :icon="Plus"
          @click="
            addOpen = true;
            addResult = [];
          "
          >添加来源</el-button
        >
      </div>
    </div>
    <el-alert
      v-if="error"
      :title="error"
      type="error"
      show-icon
      :closable="false"
    />
    <div class="library-layout">
      <aside class="source-panel">
        <div class="source-heading">
          我的来源<span>{{ sources.length }}</span>
        </div>
        <button
          class="source-link"
          :class="{ selected: !source }"
          @click="source = ''"
        >
          <Library :size="17" /><span>全部内容</span
          ><small>{{ contents.length }}</small>
        </button>
        <template
          v-for="kind in [
            'FAVORITE',
            'COLLECTION',
            'SERIES',
            'USER_ADDED',
            'MOVIE',
          ]"
          :key="kind"
          ><div class="source-group">{{ kindLabel[kind] }}</div>
          <div
            v-for="s in sources
              .filter((x) => x.kind === kind)
              .sort((a, b) => Number(b.pinned) - Number(a.pinned))"
            :key="s.id"
            class="source-row"
          >
            <button
              class="source-link"
              :class="{ selected: source === s.id }"
              @click="source = s.id"
            >
              <component
                :is="
                  kind === 'FAVORITE'
                    ? FolderHeart
                    : kind === 'SERIES'
                      ? Clapperboard
                      : Layers
                "
                :size="16"
              /><span>{{ s.alias || s.title }}</span
              ><Pin v-if="s.pinned" :size="12" /></button
            ><button
              class="source-edit icon-button"
              :aria-label="'设置来源 ' + s.title"
              @click="editSource(s)"
            >
              <Settings2 :size="14" />
            </button>
          </div>
          <p v-if="!sources.some((s) => s.kind === kind)" class="source-empty">
            尚未添加
          </p></template
        >
        <div class="source-hint">
          <Info :size="17" />
          <p>添加和刷新来源只收录信息，下载由你决定。</p>
        </div>
      </aside>
      <section class="library-content">
        <div class="content-toolbar">
          <h2>
            {{ currentSource?.alias || currentSource?.title || "全部内容" }}
            <span>{{ filtered.length }}</span>
          </h2>
          <div class="toolbar">
            <el-input
              v-model="q"
              placeholder="搜索视频标题"
              aria-label="搜索视频标题"
              clearable
              class="search-field"
              ><template #prefix><Search :size="16" /></template
            ></el-input>
            <div class="view-toggle">
              <button
                aria-label="卡片视图"
                :class="{ active: view === 'grid' }"
                @click="view = 'grid'"
              >
                <LayoutGrid :size="17" /></button
              ><button
                aria-label="列表视图"
                :class="{ active: view === 'list' }"
                @click="view = 'list'"
              >
                <List :size="17" />
              </button>
            </div>
          </div>
        </div>
        <SelectFilter
          :data="filterData"
          :default-values="{ type, state }"
          @change="
            type = $event.type;
            state = $event.state;
          "
        />
        <div class="filter-secondary">
          <div class="toolbar">
            <label
              >发布于
              <input
                type="date"
                v-model="dateFrom"
                aria-label="发布时间起始日期" /></label
            ><span>—</span
            ><input
              type="date"
              v-model="dateTo"
              aria-label="发布时间结束日期"
            /><el-button
              link
              @click="
                dateFrom = '';
                dateTo = '';
              "
              >清除日期</el-button
            >
          </div>
          <el-select v-model="sort" aria-label="排序方式" style="width: 135px"
            ><el-option label="原站顺序" value="original" /><el-option
              label="最新发布"
              value="published"
          /></el-select>
        </div>
        <div class="library-meta">
          <span
            >{{
              currentSource?.status === "NEEDS_ACTION"
                ? "来源刷新失败，保留已有信息"
                : "信息独立更新"
            }}
            ·
            {{
              formatDate(currentSource?.lastSyncedAt || contents[0]?.updatedAt)
            }}</span
          ><el-button
            link
            type="primary"
            @click="selectAll"
            :disabled="!filtered.length"
            >全选筛选结果（{{ filtered.length }}）</el-button
          >
        </div>
        <div
          v-loading="loading"
          class="video-grid"
          :class="{ 'video-list': view === 'list' }"
        >
          <article
            v-for="c in visible"
            :key="c.id"
            class="video-card"
            :class="{ chosen: isSelected(c.id) }"
          >
            <div class="video-cover">
              <a
                :href="c.originalUrl"
                target="_blank"
                rel="noopener noreferrer"
                :aria-label="'在 B 站打开 ' + c.title"
                ><img :src="c.coverUrl" :alt="c.title + ' · 演示封面'" /><span
                  class="cover-external"
                  ><ArrowUpRight :size="18" /></span></a
              ><button
                class="cover-checkbox"
                :aria-label="'选择 ' + c.title"
                :aria-pressed="isSelected(c.id)"
                @click="toggle(c.id)"
              >
                <Check v-if="isSelected(c.id)" :size="15" /></button
              ><span class="video-duration">{{
                duration(c.durationSeconds)
              }}</span
              ><span class="part-tag" v-if="c.downloadSummary.totalCount > 1"
                >{{ c.type === "SERIES" ? "剧集" : "多 P" }} ·
                {{ c.downloadSummary.totalCount }}</span
              >
            </div>
            <div class="video-info">
              <a
                class="video-title"
                :href="c.originalUrl"
                target="_blank"
                rel="noopener noreferrer"
                >{{ c.title }}</a
              >
              <div class="video-author">
                {{ c.author
                }}<span>{{
                  sources.find((s) => c.sourceIds.includes(s.id))?.alias ||
                  sources.find((s) => c.sourceIds.includes(s.id))?.title ||
                  "用户添加"
                }}</span>
              </div>
              <div class="video-status">
                <StatusBadge :state="c.downloadSummary.state" /><span
                  v-if="c.archiveSummary.state !== 'NOT_REQUESTED'"
                  class="archive-mini"
                  >云端：<StatusBadge :state="c.archiveSummary.state"
                /></span>
              </div>
              <div class="video-actions">
                <el-button link :icon="Info" @click="showDetail(c)"
                  >详情</el-button
                ><el-button
                  link
                  type="primary"
                  :icon="Download"
                  @click="openPreview(c.id)"
                  >下载</el-button
                >
              </div>
            </div>
          </article>
        </div>
        <el-empty
          v-if="!loading && !visible.length"
          description="暂无匹配内容，试试调整筛选或添加来源"
          :image-size="90"
        />
        <div class="pagination-row">
          <span class="muted">每页 6 项 · 全部内容按内容 ID 去重</span
          ><el-pagination
            v-model:current-page="page"
            :page-size="6"
            :total="filtered.length"
            layout="prev, pager, next"
          />
        </div>
        <div v-if="selectionCount" class="selection-bar">
          <div>
            <strong>已选择 {{ selectionCount }} 项</strong
            ><span>{{
              querySelection
                ? "筛选范围已固定，取消勾选可排除项目"
                : "跨页选择已保留"
            }}</span>
          </div>
          <div class="toolbar">
            <el-button text @click="clear">清空选择</el-button
            ><el-button type="primary" :icon="Download" @click="openPreview()"
              >预览下载清单</el-button
            >
          </div>
        </div>
      </section>
    </div>
    <el-dialog v-model="addOpen" title="添加 B 站信息来源" width="660px"
      ><p class="dialog-intro">
        支持收藏夹、合集、单视频、番剧和电影。每行一条链接，只收录信息。
      </p>
      <label class="field-label" for="b-links">来源链接</label
      ><el-input
        id="b-links"
        v-model="links"
        type="textarea"
        :rows="5"
        placeholder="粘贴 B 站链接，每行一条"
      />
      <div v-if="addResult.length" class="result-list">
        <div v-for="(r, i) in addResult" :key="i">
          <span class="break-anywhere">{{ r.url }}</span
          ><strong :class="r.error ? 'text-danger' : 'text-primary'">{{
            r.error?.message || (r.reused ? "来源已存在" : "解析已排队")
          }}</strong>
        </div>
      </div>
      <template #footer
        ><el-button @click="addOpen = false">关闭</el-button
        ><el-button type="primary" :loading="adding" @click="addSources"
          >加入信息来源</el-button
        ></template
      ></el-dialog
    >
    <el-dialog v-model="sourceOpen" title="管理来源" width="490px"
      ><label class="field-label">显示别名</label
      ><el-input v-model="alias" placeholder="保留原名称时留空" />
      <div class="setting-inline">
        <span>置顶来源</span
        ><el-switch v-model="pinned" aria-label="置顶来源" />
      </div>
      <p class="muted">此处只管理来源关系和显示属性。</p>
      <template #footer
        ><el-button type="danger" plain @click="removeSource"
          >移除来源</el-button
        ><el-button @click="sourceOpen = false">取消</el-button
        ><el-button type="primary" @click="saveSource"
          >保存</el-button
        ></template
      ></el-dialog
    >
    <el-drawer v-model="detailOpen" title="内容详情" size="520px"
      ><template v-if="detail"
        ><img class="detail-cover" :src="detail.coverUrl" alt="演示封面" />
        <h2>{{ detail.title }}</h2>
        <p class="muted">
          {{ detail.author }} · {{ duration(detail.durationSeconds) }}
        </p>
        <p class="detail-description">{{ detail.description }}</p>
        <dl class="detail-facts">
          <dt>原始标识</dt>
          <dd>{{ detail.externalId }}</dd>
          <dt>信息更新时间</dt>
          <dd>{{ formatDate(detail.updatedAt) }}</dd>
          <dt>下载状态</dt>
          <dd><StatusBadge :state="detail.downloadSummary.state" /></dd>
          <dt>归档状态</dt>
          <dd>
            <StatusBadge
              :state="detail.archiveSummary.state"
              :label="
                detail.archiveSummary.state === 'NOT_REQUESTED'
                  ? '尚未归档'
                  : undefined
              "
            />
          </dd>
          <dt>来源关系</dt>
          <dd>
            {{
              sources
                .filter((s) => detail!.sourceIds.includes(s.id))
                .map((s) => s.alias || s.title)
                .join("、") || "来源已移除"
            }}
          </dd>
        </dl>
        <el-alert
          v-if="detail.availability !== 'AVAILABLE'"
          :title="detail.availabilityReason"
          type="warning"
          :closable="false"
        />
        <h3>
          {{ detail.type === "SERIES" ? "第一季 · 选择剧集" : "选择媒体部分" }}
        </h3>
        <el-checkbox-group v-model="partIds" class="part-list"
          ><el-checkbox v-for="p in parts" :key="p.id" :value="p.id"
            >{{ p.title }}
            <span class="muted">{{
              duration(p.durationSeconds)
            }}</span></el-checkbox
          ></el-checkbox-group
        ><el-button
          type="primary"
          :icon="Download"
          :disabled="!partIds.length"
          @click="
            previewSelection = {
              mode: 'EXPLICIT',
              resource: 'BILIBILI_PARTS',
              ids: [...partIds],
            };
            previewOpen = true;
          "
          >预览所选部分</el-button
        >
        <div class="detail-links">
          <a
            :href="detail.originalUrl"
            target="_blank"
            rel="noopener noreferrer"
            >在 B 站打开 <ExternalLink :size="14" /></a
          ><button
            @click="
              detailOpen = false;
              router.push(
                { path: '/downloads', query: { platform: 'BILIBILI', contentId: detail.id } },
              );
            "
          >
            查看下载记录</button
          ><button
            @click="
              detailOpen = false;
              router.push('/archives?platform=BILIBILI');
            "
          >
            查看归档
          </button>
        </div></template
      ></el-drawer
    >
    <DownloadPreview
      v-model="previewOpen"
      platform="BILIBILI"
      :selection="previewSelection"
    />
  </div>
</template>
