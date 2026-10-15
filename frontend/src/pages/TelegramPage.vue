<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import {
  Archive,
  ArrowDownToLine,
  Check,
  ChevronDown,
  ChevronRight,
  CircleAlert,
  Clock3,
  Download,
  FileJson2,
  FileText,
  Link2,
  ListFilter,
  LoaderCircle,
  MessageSquareText,
  RefreshCw,
  Search,
  ShieldCheck,
  Sparkles,
  Video,
} from "lucide-vue-next";
import StatusBadge from "../components/StatusBadge.vue";
import { api, dataRevision, notifyError, notifySuccess } from "../api/client";
import { formatBytes, formatDate } from "../format";

import { useRoute } from "vue-router";
import {
  canonicalizeTelegramLink as canonicalizeLink,
  isTelegramMessageLink,
} from "../api/telegram-links";
const route = useRoute();
type RecordData = Record<string, any>;
type TextSegment = { text: string; href?: string };

const accountId = "tg-main";
const activeTab = ref<"messages" | "links" | "resources">("messages");
const messages = ref<RecordData[]>([]);
const resources = ref<RecordData[]>([]);
const watcher = ref<RecordData | null>(null);
const activeTask = ref<RecordData | null>(null);
const totalMessages = ref<number | null>(null);
const totalResources = ref<number | null>(null);
const nextResourceCursor = ref<string | null>(null);
const expandedMessages = ref(new Set<string>());
const messageDetails = ref<Record<string, RecordData>>({});
const pageError = ref("");
const loading = ref(false);
const taskLoading = ref(false);
const watcherSaving = ref(false);
const syncSubmitting = ref(false);
const retrySubmitting = ref(false);
const retryingResourceIds = ref(new Set<string>());
const messageQuery = ref("");
const resourceQuery = ref("");
const resourceState = ref("");
const linkInput = ref("");
const showSavedSyncDialog = ref(false);
const showLinkConfirmDialog = ref(false);
const detailVisible = ref(false);
const selectedResource = ref<RecordData | null>(null);
const detailLoading = ref(false);
let requestSequence = 0;
let filterTimer: ReturnType<typeof setTimeout> | undefined;

const scanState = computed<RecordData>(() => watcher.value?.scanState ?? {});
const initialScanCompleted = computed(
  () => scanState.value.initialScanCompleted === true,
);
const watcherEnabled = computed(() => watcher.value?.enabled === true);
const syncTask = computed<RecordData | null>(() => activeTask.value);
const linkTokens = computed(() =>
  linkInput.value
    .split(/\s+/)
    .map((value) => value.trim())
    .filter(Boolean),
);
const parsedLinks = computed(() => {
  const unique: string[] = [];
  const invalid: string[] = [];
  const seen = new Set<string>();
  for (const token of linkTokens.value) {
    const canonical = canonicalizeLink(token);
    if (!isTelegramMessageLink(token)) {
      invalid.push(token);
      continue;
    }
    if (!seen.has(canonical)) {
      seen.add(canonical);
      unique.push(token);
    }
  }
  return {
    unique,
    invalid,
    duplicateCount: linkTokens.value.length - unique.length - invalid.length,
  };
});
const linksExceedLimit = computed(() => linkTokens.value.length > 200);
const linkSubmissionReady = computed(
  () =>
    parsedLinks.value.unique.length > 0 &&
    parsedLinks.value.invalid.length === 0 &&
    !linksExceedLimit.value,
);
const resourceSummary = computed(() => {
  if (totalResources.value == null)
    return "已显示 " + resources.value.length + " 条";
  return "共 " + totalResources.value + " 条";
});

function idOf(value: RecordData) {
  return String(value.id ?? value.externalId ?? "");
}

function messageText(message: RecordData) {
  return String(
    messageDetails.value[idOf(message)]?.text ?? message.text ?? "",
  );
}

function textSegments(value: string): TextSegment[] {
  const result: TextSegment[] = [];
  const matcher = /https?:\/\/[^\s<>"']+/gi;
  let cursor = 0;
  let match: RegExpExecArray | null;
  while ((match = matcher.exec(value)) !== null) {
    const index = match.index;
    if (index > cursor) result.push({ text: value.slice(cursor, index) });
    result.push({ text: match[0], href: match[0] });
    cursor = index + match[0].length;
  }
  if (cursor < value.length) result.push({ text: value.slice(cursor) });
  return result;
}

function displayDate(value: unknown) {
  if (
    typeof value !== "string" ||
    !value.trim() ||
    Number.isNaN(new Date(value).getTime())
  ) {
    return "时间待处理";
  }
  return formatDate(value);
}

function formatMediaType(value: unknown) {
  if (typeof value !== "string" || !value) return "类型未知";
  if (value.startsWith("video/")) return "视频";
  if (value.startsWith("audio/")) return "音频";
  if (value.startsWith("image/")) return "图片";
  return value;
}

function attachmentState(attachment: RecordData) {
  return attachment.downloadState ?? attachment.state ?? "UNKNOWN";
}

function attachmentName(attachment: RecordData) {
  return attachment.name || "附件名称未知";
}

function messageDownloadState(message: RecordData) {
  return message.downloadSummary?.state;
}

function progressPercent(progress: RecordData | undefined) {
  if (
    !progress ||
    progress.totalItems == null ||
    Number(progress.totalItems) <= 0
  )
    return null;
  return Math.min(
    100,
    Math.max(
      0,
      Math.round(
        (Number(progress.doneItems ?? 0) / Number(progress.totalItems)) * 100,
      ),
    ),
  );
}

function progressLabel(progress: RecordData | undefined) {
  if (!progress) return "正在等待进度信息";
  const parts: string[] = [];
  if (progress.totalItems != null)
    parts.push((progress.doneItems ?? 0) + " / " + progress.totalItems + " 项");
  if (progress.doneBytes != null) {
    const total =
      progress.totalBytes == null
        ? "总大小未知"
        : formatBytes(progress.totalBytes);
    parts.push(formatBytes(progress.doneBytes) + " / " + total);
  }
  if (progress.bytesPerSecond != null)
    parts.push(formatBytes(progress.bytesPerSecond) + "/秒");
  return parts.length ? parts.join(" · ") : "正在处理，项目总量待确认";
}

function pagePayload(value: any) {
  return value && Array.isArray(value.items)
    ? {
        items: value.items as RecordData[],
        total: typeof value.total === "number" ? value.total : null,
        nextCursor: value.nextCursor ?? null,
      }
    : {
        items: [] as RecordData[],
        total: null as number | null,
        nextCursor: null as string | null,
      };
}

function queryString(values: Record<string, string | undefined>) {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(values))
    if (value) params.set(key, value);
  const suffix = params.toString();
  return suffix ? "?" + suffix : "";
}

async function loadData(appendResources = false) {
  const sequence = ++requestSequence;
  const currentTab = activeTab.value;
  loading.value = true;
  pageError.value = "";
  try {
    const watcherRequest = api.get<RecordData>(
      "/telegram/watcher?accountId=" + encodeURIComponent(accountId),
    );
    if (currentTab === "messages") {
      const messagePath =
        "/telegram/messages" +
        queryString({ q: messageQuery.value.trim() || undefined });
      const [watcherData, messageData] = await Promise.all([
        watcherRequest,
        api.get<RecordData>(messagePath),
      ]);
      if (sequence !== requestSequence) return;
      watcher.value = watcherData;
      const page = pagePayload(messageData);
      messages.value = page.items;
      totalMessages.value = page.total;
      await loadCurrentTask(watcherData, sequence);
    } else if (currentTab === "resources") {
      const cursor = appendResources
        ? (nextResourceCursor.value ?? undefined)
        : undefined;
      const resourcePath =
        "/telegram/resources" +
        queryString({
          q: resourceQuery.value.trim() || undefined,
          state: resourceState.value || undefined,
          cursor,
        });
      const [watcherData, resourceData] = await Promise.all([
        watcherRequest,
        api.get<RecordData>(resourcePath),
      ]);
      if (sequence !== requestSequence) return;
      watcher.value = watcherData;
      const page = pagePayload(resourceData);
      resources.value = appendResources
        ? [...resources.value, ...page.items]
        : page.items;
      totalResources.value = page.total;
      nextResourceCursor.value = page.nextCursor;
      await loadCurrentTask(watcherData, sequence);
    } else {
      const watcherData = await watcherRequest;
      if (sequence !== requestSequence) return;
      watcher.value = watcherData;
      await loadCurrentTask(watcherData, sequence);
    }
  } catch (error) {
    if (sequence !== requestSequence) return;
    pageError.value =
      error instanceof Error ? error.message : "页面信息读取失败，请重试。";
    notifyError(error);
  } finally {
    if (sequence === requestSequence) loading.value = false;
  }
}

async function loadCurrentTask(watcherData: RecordData, sequence: number) {
  const taskId = watcherData?.scanState?.activeTaskId;
  if (!taskId) {
    activeTask.value = null;
    return;
  }
  taskLoading.value = true;
  try {
    const task = await api.get<RecordData>(
      "/tasks/" + encodeURIComponent(String(taskId)),
    );
    if (sequence === requestSequence) activeTask.value = task;
  } finally {
    if (sequence === requestSequence) taskLoading.value = false;
  }
}

function scheduleLoad() {
  if (filterTimer) clearTimeout(filterTimer);
  filterTimer = setTimeout(() => {
    void loadData();
  }, 280);
}

watch(dataRevision, () => {
  void loadData();
});
watch(
  () => route.query.q,
  (value) => {
    if (route.path === "/telegram" && value) {
      activeTab.value = "resources";
      resourceQuery.value = String(value);
    }
  },
  { immediate: true },
);
watch([activeTab, messageQuery, resourceQuery, resourceState], () => {
  nextResourceCursor.value = null;
  scheduleLoad();
});
onMounted(() => {
  void loadData();
});
onUnmounted(() => {
  if (filterTimer) clearTimeout(filterTimer);
});

function isExpanded(message: RecordData) {
  return expandedMessages.value.has(idOf(message));
}

async function toggleMessage(message: RecordData) {
  const id = idOf(message);
  const next = new Set(expandedMessages.value);
  if (next.has(id)) {
    next.delete(id);
    expandedMessages.value = next;
    return;
  }
  next.add(id);
  expandedMessages.value = next;
  if (!messageDetails.value[id]) {
    try {
      const detail = await api.get<RecordData>(
        "/telegram/messages/" + encodeURIComponent(id),
      );
      messageDetails.value = { ...messageDetails.value, [id]: detail };
    } catch (error) {
      notifyError(error);
      const rollback = new Set(expandedMessages.value);
      rollback.delete(id);
      expandedMessages.value = rollback;
    }
  }
}

async function submitSavedSync() {
  syncSubmitting.value = true;
  try {
    const result = await api.post<RecordData>("/telegram/saved-sync", {
      accountId,
    });
    showSavedSyncDialog.value = false;
    const taskId = result.taskId;
    notifySuccess(
      taskId
        ? "收藏同步已受理，任务 " + taskId + " 已排队，等待执行。"
        : "收藏同步已受理，已排队，等待执行。",
    );
  } catch (error) {
    notifyError(error);
  } finally {
    syncSubmitting.value = false;
  }
}

function openSavedSync() {
  if (!initialScanCompleted.value) showSavedSyncDialog.value = true;
  else void submitSavedSync();
}

async function toggleWatcher(enabled: boolean | string | number) {
  const nextEnabled = Boolean(enabled);
  if (nextEnabled && !initialScanCompleted.value) return;
  watcherSaving.value = true;
  try {
    await api.patch("/telegram/watcher", { accountId, enabled: nextEnabled });
    notifySuccess(nextEnabled ? "收藏监控已启用。" : "收藏监控已暂停。");
  } catch (error) {
    notifyError(error);
  } finally {
    watcherSaving.value = false;
  }
}

function openLinkConfirmation() {
  if (linkSubmissionReady.value) showLinkConfirmDialog.value = true;
}

async function submitLinks() {
  if (!linkSubmissionReady.value) return;
  syncSubmitting.value = true;
  try {
    const result = await api.post<RecordData>("/telegram/link-downloads", {
      accountId,
      links: parsedLinks.value.unique,
    });
    showLinkConfirmDialog.value = false;
    const taskId = result.taskId;
    notifySuccess(
      taskId
        ? "消息链接任务 " + taskId + " 已受理，已排队等待解析与下载。"
        : "消息链接任务已受理，已排队等待解析与下载。",
    );
    linkInput.value = "";
  } catch (error) {
    notifyError(error);
  } finally {
    syncSubmitting.value = false;
  }
}

async function retryIncomplete(resourceId?: string) {
  if (resourceId) {
    const next = new Set(retryingResourceIds.value);
    next.add(resourceId);
    retryingResourceIds.value = next;
  } else retrySubmitting.value = true;
  try {
    const result = await api.post<RecordData>("/telegram/retry-incomplete", {
      accountId,
      ...(resourceId ? { resourceIds: [resourceId] } : {}),
    });
    const taskId = result.taskId;
    notifySuccess(
      taskId
        ? "补齐任务 " + taskId + " 已受理，已排队，等待执行。"
        : "补齐未完成文件的任务已受理。",
    );
  } catch (error) {
    notifyError(error);
  } finally {
    if (resourceId) {
      const next = new Set(retryingResourceIds.value);
      next.delete(resourceId);
      retryingResourceIds.value = next;
    } else retrySubmitting.value = false;
  }
}

function canRetry(resource: RecordData) {
  return (
    Number(resource.failedCount ?? 0) > 0 ||
    Number(resource.completedCount ?? 0) < Number(resource.mediaCount ?? 0) ||
    ["PARTIAL", "NEEDS_ACTION", "FAILED"].includes(String(resource.state ?? ""))
  );
}

async function openResource(resource: RecordData) {
  selectedResource.value = resource;
  detailVisible.value = true;
  detailLoading.value = true;
  try {
    const detail = await api.get<RecordData>(
      "/telegram/resources/" + encodeURIComponent(idOf(resource)),
    );
    selectedResource.value = detail;
  } catch (error) {
    notifyError(error);
  } finally {
    detailLoading.value = false;
  }
}

async function exportMetadata(resource: RecordData) {
  try {
    const result = await api.get<RecordData>(
      "/telegram/resources/" + encodeURIComponent(idOf(resource)) + "/metadata",
    );
    const content = JSON.stringify(result, null, 2);
    const blob = new Blob([content], {
      type: "application/json;charset=utf-8",
    });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    const baseName = String(
      resource.directoryLabel || "telegram-resource-" + idOf(resource),
    )
      .replace(/[<>:"/\\|?*\u0000-\u001f]/g, "_")
      .slice(0, 100);
    anchor.href = url;
    anchor.download = (baseName || "telegram-resource") + ".metadata.json";
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    URL.revokeObjectURL(url);
    notifySuccess("整帖元数据 JSON 已在浏览器中导出。");
  } catch (error) {
    notifyError(error);
  }
}

async function loadMoreResources() {
  if (!nextResourceCursor.value || loading.value) return;
  await loadData(true);
}

function resourceDate(resource: RecordData) {
  return displayDate(resource.sentAt ?? resource.createdAt);
}

function scanFailure() {
  const failure = scanState.value.error ?? watcher.value?.error;
  if (!failure) return "";
  return typeof failure === "string"
    ? failure
    : (failure.message ?? "最近扫描未完成，请查看任务记录。");
}
</script>
<template>
  <main class="telegram-page">
    <header class="page-header telegram-header">
      <div class="telegram-heading">
        <div class="telegram-brand-mark" aria-hidden="true">
          <MessageSquareText :size="21" />
        </div>
        <div>
          <h1 class="page-title">Telegram 收藏</h1>
          <p class="page-subtitle">
            浏览已收录消息，按需补扫收藏或提交消息链接。
          </p>
        </div>
      </div>
      <el-button
        class="saved-sync-button"
        type="primary"
        :loading="syncSubmitting"
        @click="openSavedSync"
      >
        <template #icon><ArrowDownToLine :size="16" /></template>
        下载收藏增量
      </el-button>
    </header>

    <el-alert
      v-if="pageError"
      class="telegram-page-alert"
      type="error"
      :closable="true"
      show-icon
      @close="pageError = ''"
    >
      <template #title>页面数据可能已过期</template>
      {{ pageError }}
      <el-button link type="primary" @click="loadData()">重新读取</el-button>
    </el-alert>

    <nav class="telegram-tabs" aria-label="Telegram 页面">
      <button
        class="telegram-tab"
        :class="{ active: activeTab === 'messages' }"
        @click="activeTab = 'messages'"
      >
        <MessageSquareText :size="16" />收藏消息
      </button>
      <button
        class="telegram-tab"
        :class="{ active: activeTab === 'links' }"
        @click="activeTab = 'links'"
      >
        <Link2 :size="16" />消息链接下载
      </button>
      <button
        class="telegram-tab"
        :class="{ active: activeTab === 'resources' }"
        @click="activeTab = 'resources'"
      >
        <Archive :size="16" />资源库
      </button>
    </nav>

    <div class="telegram-layout">
      <section class="telegram-main-panel">
        <template v-if="activeTab === 'messages'">
          <div class="telegram-section-top">
            <div>
              <h2 class="section-title">收藏消息流</h2>
              <p class="muted">
                原文与附件分别保留状态；展开三角可查看完整正文。
              </p>
            </div>
            <el-input
              v-model="messageQuery"
              class="telegram-search"
              clearable
              placeholder="搜索消息正文"
              aria-label="搜索消息正文"
            >
              <template #prefix><Search :size="15" /></template>
            </el-input>
          </div>

          <div v-if="loading && !messages.length" class="telegram-loading">
            <el-skeleton :rows="5" animated />
          </div>
          <div v-else-if="!messages.length" class="empty-state telegram-empty">
            <MessageSquareText :size="24" />
            <strong>还没有已收录的收藏消息</strong>
            <span
              >打开页面不会访问
              Telegram；点击“下载收藏增量”后才会开始同步。</span
            >
          </div>
          <div v-else class="message-stream">
            <article
              v-for="message in messages"
              :key="idOf(message)"
              class="message-card"
            >
              <button
                class="message-expand"
                :aria-expanded="isExpanded(message)"
                :aria-label="
                  isExpanded(message)
                    ? '收起消息正文和附件'
                    : '展开消息正文和附件'
                "
                @click="toggleMessage(message)"
              >
                <ChevronDown v-if="isExpanded(message)" :size="18" />
                <ChevronRight v-else :size="18" />
              </button>
              <div class="message-card-content">
                <div class="message-meta">
                  <span class="message-time"
                    ><Clock3 :size="14" />{{
                      displayDate(message.sentAt)
                    }}</span
                  >
                  <span v-if="message.groupId" class="message-group"
                    >媒体组</span
                  >
                  <span class="message-attachment-count"
                    >{{ (message.attachments ?? []).length }} 个附件</span
                  >
                  <StatusBadge
                    v-if="messageDownloadState(message)"
                    :state="messageDownloadState(message)"
                  />
                </div>
                <button class="message-preview" @click="toggleMessage(message)">
                  {{ message.text || "此消息没有文字内容" }}
                </button>
                <div v-if="isExpanded(message)" class="message-expanded">
                  <div class="full-message-text" aria-label="完整消息正文">
                    <template
                      v-for="(segment, index) in textSegments(
                        messageText(message),
                      )"
                      :key="idOf(message) + '-' + index"
                    >
                      <a
                        v-if="segment.href"
                        :href="segment.href"
                        target="_blank"
                        rel="noreferrer noopener"
                        >{{ segment.text }}</a
                      >
                      <span v-else>{{ segment.text }}</span>
                    </template>
                    <span v-if="!messageText(message)"
                      >此消息没有文字内容。</span
                    >
                  </div>
                  <div v-if="message.sourceLinks?.length" class="source-links">
                    <a
                      v-for="(link, index) in message.sourceLinks"
                      :key="idOf(message) + '-link-' + index"
                      :href="link"
                      target="_blank"
                      rel="noreferrer noopener"
                    >
                      <Link2 :size="13" />{{ link }}
                    </a>
                  </div>
                  <div
                    v-if="message.attachments?.length"
                    class="attachment-list"
                  >
                    <div
                      v-for="attachment in message.attachments"
                      :key="idOf(attachment)"
                      class="attachment-row"
                    >
                      <div class="attachment-icon" aria-hidden="true">
                        <Video
                          v-if="
                            String(attachment.mediaType ?? '').startsWith(
                              'video/',
                            )
                          "
                          :size="16"
                        />
                        <FileText v-else :size="16" />
                      </div>
                      <div class="attachment-copy">
                        <strong>{{ attachmentName(attachment) }}</strong>
                        <span
                          >{{ formatMediaType(attachment.mediaType) }} ·
                          {{ formatBytes(attachment.sizeBytes) }}</span
                        >
                      </div>
                      <StatusBadge :state="attachmentState(attachment)" />
                    </div>
                  </div>
                  <p v-else class="muted no-attachments">此消息没有附件。</p>
                </div>
              </div>
            </article>
          </div>
          <footer v-if="messages.length" class="list-footer">
            <span class="muted">{{
              totalMessages == null
                ? "已显示 " + messages.length + " 条消息"
                : "共 " + totalMessages + " 条消息"
            }}</span>
            <span v-if="loading" class="muted"
              ><LoaderCircle class="spin" :size="15" />正在更新</span
            >
          </footer>
        </template>

        <template v-else-if="activeTab === 'links'">
          <div class="telegram-section-top">
            <div>
              <h2 class="section-title">按消息链接下载</h2>
              <p class="muted">
                按单帖或完整媒体组处理，支持公开与私有链接、主题及评论串。
              </p>
            </div>
            <span class="links-limit"
              ><ShieldCheck :size="15" />每批最多 200 条</span
            >
          </div>
          <div class="link-download-card">
            <label class="input-label" for="telegram-links"
              >粘贴 Telegram 消息链接</label
            >
            <el-input
              id="telegram-links"
              v-model="linkInput"
              type="textarea"
              :rows="8"
              resize="vertical"
              placeholder="每行一条，也可用空格或换行分隔&#10;https://t.me/example/123"
              aria-label="Telegram 消息链接"
            />
            <div class="link-input-footer">
              <span
                >{{ parsedLinks.unique.length }} 条不同链接<span
                  v-if="parsedLinks.duplicateCount"
                >
                  · 已去重 {{ parsedLinks.duplicateCount }} 条</span
                ></span
              >
              <el-button
                type="primary"
                :disabled="!linkSubmissionReady"
                @click="openLinkConfirmation"
              >
                <template #icon><Download :size="16" /></template>
                确认链接并继续
              </el-button>
            </div>
            <div
              v-if="parsedLinks.invalid.length"
              class="validation-note validation-error"
            >
              <CircleAlert :size="16" />
              <div>
                <strong>发现无法识别的链接，整批暂不提交</strong>
                <ul>
                  <li
                    v-for="(item, index) in parsedLinks.invalid.slice(0, 5)"
                    :key="item + '-' + index"
                  >
                    {{ item }}
                  </li>
                </ul>
                <span v-if="parsedLinks.invalid.length > 5"
                  >另有 {{ parsedLinks.invalid.length - 5 }} 条无效链接。</span
                >
              </div>
            </div>
            <div
              v-if="linksExceedLimit"
              class="validation-note validation-error"
            >
              <CircleAlert :size="16" />输入共
              {{ linkTokens.length }} 条，超过本批上限 200 条。
            </div>
            <div
              v-if="
                linkInput &&
                !parsedLinks.invalid.length &&
                !linksExceedLimit &&
                parsedLinks.unique.length
              "
              class="validation-note validation-ok"
            >
              <Check :size="16" />
              <span
                >链接语法检查通过；本次会解析并下载这些消息目标。目标媒体会按帖或媒体组归档。</span
              >
            </div>
            <div class="link-scope-note">
              <Sparkles :size="15" />
              <span
                >只处理本次明确提交的消息目标，不会抓取整个频道。单条链接访问失败不会阻断其他链接。</span
              >
            </div>
          </div>
        </template>

        <template v-else>
          <div class="telegram-section-top resource-heading">
            <div>
              <h2 class="section-title">Telegram 资源库</h2>
              <p class="muted">
                {{ resourceSummary }} · 每条记录对应一个帖子或媒体组。
              </p>
            </div>
            <el-button :loading="retrySubmitting" @click="retryIncomplete()">
              <template #icon><RefreshCw :size="15" /></template>
              补齐全部未完成文件
            </el-button>
          </div>
          <div class="resource-filters">
            <el-input
              v-model="resourceQuery"
              class="telegram-search"
              clearable
              placeholder="搜索正文、频道或文件名"
              aria-label="搜索资源"
            >
              <template #prefix><Search :size="15" /></template>
            </el-input>
            <el-select
              v-model="resourceState"
              clearable
              placeholder="全部状态"
              aria-label="按状态筛选"
            >
              <el-option label="全部状态" value="" />
              <el-option label="部分完成" value="PARTIAL" />
              <el-option label="需要处理" value="NEEDS_ACTION" />
              <el-option label="失败" value="FAILED" />
              <el-option label="排队中" value="QUEUED" />
              <el-option label="进行中" value="RUNNING" />
              <el-option label="已验证" value="VERIFIED" />
            </el-select>
            <span class="filter-caption"
              ><ListFilter :size="14" />搜索与状态筛选</span
            >
          </div>

          <div v-if="loading && !resources.length" class="telegram-loading">
            <el-skeleton :rows="4" animated />
          </div>
          <div v-else-if="!resources.length" class="empty-state telegram-empty">
            <Archive :size="24" />
            <strong>没有匹配的 Telegram 资源</strong>
            <span>调整搜索条件，或通过消息链接添加需要下载的帖子。</span>
          </div>
          <div v-else class="resource-list">
            <article
              v-for="resource in resources"
              :key="idOf(resource)"
              class="resource-card"
            >
              <button class="resource-main" @click="openResource(resource)">
                <div class="resource-icon"><Archive :size="17" /></div>
                <div class="resource-copy">
                  <strong>{{
                    resource.channel ||
                    resource.directoryLabel ||
                    "Telegram 资源 " + idOf(resource)
                  }}</strong>
                  <span class="resource-subtitle">{{
                    resource.authorTags?.length
                      ? resource.authorTags.join(" · ")
                      : "来源信息未提供"
                  }}</span>
                  <span class="resource-date"
                    >{{ resourceDate(resource) }} ·
                    {{
                      resource.mediaCount ?? resource.attachments?.length ?? 0
                    }}
                    个附件 · 完成 {{ resource.completedCount ?? 0 }}</span
                  >
                </div>
                <div class="resource-status">
                  <StatusBadge :state="resource.state ?? 'UNKNOWN'" />
                  <span v-if="resource.failedCount"
                    >失败 {{ resource.failedCount }}</span
                  >
                </div>
                <ChevronRight :size="17" class="resource-chevron" />
              </button>
              <div class="resource-actions">
                <el-button
                  link
                  type="primary"
                  @click.stop="exportMetadata(resource)"
                >
                  <template #icon><FileJson2 :size="15" /></template>导出元数据
                  JSON
                </el-button>
                <el-button
                  v-if="canRetry(resource)"
                  link
                  type="primary"
                  :loading="retryingResourceIds.has(idOf(resource))"
                  @click.stop="retryIncomplete(idOf(resource))"
                >
                  <template #icon><RefreshCw :size="15" /></template>补齐本帖
                </el-button>
              </div>
            </article>
          </div>
          <footer
            v-if="resources.length || nextResourceCursor"
            class="list-footer"
          >
            <span class="muted">{{ resourceSummary }}</span>
            <el-button
              v-if="nextResourceCursor"
              text
              type="primary"
              :loading="loading"
              @click="loadMoreResources"
            >
              加载更多<ChevronDown :size="15" />
            </el-button>
          </footer>
        </template>
      </section>

      <aside class="watcher-panel">
        <div class="watcher-panel-top">
          <div class="watcher-icon"><RefreshCw :size="17" /></div>
          <div>
            <h2>收藏监控</h2>
            <p>每 {{ watcher?.intervalSeconds ?? 60 }} 秒检查一次新收藏</p>
          </div>
          <el-switch
            :model-value="watcherEnabled"
            :loading="watcherSaving"
            :disabled="
              watcherSaving || (!initialScanCompleted && !watcherEnabled)
            "
            aria-label="启用收藏监控"
            @change="toggleWatcher"
          />
        </div>
        <div v-if="!initialScanCompleted" class="watcher-prerequisite">
          <CircleAlert :size="16" />
          <span>首次全历史扫描完成后，才能启用收藏监控。</span>
        </div>
        <div class="watcher-stats">
          <div class="watcher-stat">
            <span>监控状态</span>
            <StatusBadge
              :state="watcherEnabled ? 'ACTIVE' : 'PAUSED'"
              :label="watcherEnabled ? '已启用' : '已暂停'"
            />
          </div>
          <div class="watcher-stat">
            <span>下次检查</span>
            <strong>{{ displayDate(watcher?.nextCheckAt) }}</strong>
          </div>
          <div class="watcher-stat">
            <span>最近检查</span>
            <strong>{{ displayDate(watcher?.lastCheckedAt) }}</strong>
          </div>
          <div class="watcher-stat">
            <span>已扫描到</span>
            <strong>{{ displayDate(scanState.scannedThroughAt) }}</strong>
          </div>
          <div v-if="scanState.cursorMessageId" class="watcher-stat">
            <span>最后完整消息 ID</span
            ><strong>{{ scanState.cursorMessageId }}</strong>
          </div>
        </div>
        <div v-if="syncTask" class="sync-progress">
          <div class="sync-progress-title">
            <span
              ><LoaderCircle
                v-if="taskLoading"
                class="spin"
                :size="15"
              /><ArrowDownToLine v-else :size="15" />收藏同步进度</span
            >
            <StatusBadge :state="syncTask.status ?? 'RUNNING'" />
          </div>
          <p>{{ syncTask.phase || "正在处理收藏" }}</p>
          <el-progress
            v-if="progressPercent(syncTask.progress) != null"
            :percentage="progressPercent(syncTask.progress) ?? 0"
            :stroke-width="6"
            :show-text="false"
          />
          <el-progress
            v-else
            :percentage="42"
            :indeterminate="true"
            :duration="2"
            :stroke-width="6"
            :show-text="false"
          />
          <small>{{ progressLabel(syncTask.progress) }}</small>
        </div>
        <div v-if="scanFailure()" class="watcher-error">
          <CircleAlert :size="15" /><span>{{ scanFailure() }}</span>
        </div>
        <div class="watcher-note">
          <ShieldCheck :size="15" />
          <span>打开页面只读取已保存的数据，不会发起 Telegram 扫描。</span>
        </div>
      </aside>
    </div>

    <el-dialog
      v-model="showSavedSyncDialog"
      title="首次下载收藏增量"
      width="520px"
      class="telegram-dialog"
    >
      <div class="confirm-content">
        <div class="confirm-icon"><ArrowDownToLine :size="20" /></div>
        <div>
          <strong>将扫描全部可访问的收藏历史</strong>
          <p>
            首次同步从消息 ID 1
            开始，收录完整历史，并下载目标音频和视频附件。普通文件不因这次操作自动下载。
          </p>
          <p>
            同步会在后台运行；页面关闭后任务仍会继续。确认后将创建一个收藏同步任务。
          </p>
        </div>
      </div>
      <template #footer>
        <el-button @click="showSavedSyncDialog = false">稍后</el-button>
        <el-button
          type="primary"
          :loading="syncSubmitting"
          @click="submitSavedSync"
          >确认并开始扫描</el-button
        >
      </template>
    </el-dialog>

    <el-dialog
      v-model="showLinkConfirmDialog"
      title="确认消息链接下载"
      width="540px"
      class="telegram-dialog"
    >
      <div class="confirm-content">
        <div class="confirm-icon"><Link2 :size="20" /></div>
        <div>
          <strong
            >本次将解析并下载 {{ parsedLinks.unique.length }} 个消息目标</strong
          >
          <p>
            每个目标按单帖或完整媒体组处理。若某条链接无法访问，其他有效目标仍会继续处理。
          </p>
          <p>本次提交只覆盖文本框中的消息链接，不会抓取整个频道。</p>
        </div>
      </div>
      <template #footer>
        <el-button @click="showLinkConfirmDialog = false">返回修改</el-button>
        <el-button type="primary" :loading="syncSubmitting" @click="submitLinks"
          >确认提交</el-button
        >
      </template>
    </el-dialog>

    <el-drawer
      v-model="detailVisible"
      title="资源详情"
      size="520px"
      class="telegram-drawer"
    >
      <div
        v-if="detailLoading && !selectedResource?.attachments"
        class="telegram-loading"
      >
        <el-skeleton :rows="6" animated />
      </div>
      <template v-else-if="selectedResource">
        <div class="resource-detail-heading">
          <span class="resource-detail-eyebrow">Telegram · 帖子或媒体组</span>
          <h2>
            {{
              selectedResource.channel ||
              selectedResource.directoryLabel ||
              "资源 " + idOf(selectedResource)
            }}
          </h2>
          <div class="detail-badges">
            <StatusBadge :state="selectedResource.state ?? 'UNKNOWN'" />
            <span>{{ resourceDate(selectedResource) }}</span>
          </div>
        </div>
        <div class="detail-summary-grid">
          <div>
            <span>资源 ID</span><strong>{{ selectedResource.id }}</strong>
          </div>
          <div>
            <span>关联消息</span
            ><strong>{{ selectedResource.messageIds?.length ?? 0 }}</strong>
          </div>
          <div>
            <span>附件总数</span
            ><strong>{{
              selectedResource.mediaCount ??
              selectedResource.attachments?.length ??
              0
            }}</strong>
          </div>
          <div>
            <span>已完成</span
            ><strong>{{ selectedResource.completedCount ?? 0 }}</strong>
          </div>
          <div>
            <span>失败</span
            ><strong>{{ selectedResource.failedCount ?? 0 }}</strong>
          </div>
        </div>
        <section class="detail-section">
          <h3>消息正文</h3>
          <p class="detail-message">
            {{ selectedResource.text || "没有可展示的正文。" }}
          </p>
          <div v-if="selectedResource.sourceLinks?.length" class="source-links">
            <a
              v-for="(link, index) in selectedResource.sourceLinks"
              :key="'detail-' + index"
              :href="link"
              target="_blank"
              rel="noreferrer noopener"
            >
              <Link2 :size="13" />{{ link }}
            </a>
          </div>
        </section>
        <section class="detail-section">
          <h3>
            附件状态
            <span>{{ selectedResource.attachments?.length ?? 0 }}</span>
          </h3>
          <div
            v-if="selectedResource.attachments?.length"
            class="attachment-list"
          >
            <div
              v-for="attachment in selectedResource.attachments"
              :key="idOf(attachment)"
              class="attachment-row"
            >
              <div class="attachment-icon"><FileText :size="16" /></div>
              <div class="attachment-copy">
                <strong>{{ attachmentName(attachment) }}</strong>
                <span
                  >{{ formatMediaType(attachment.mediaType) }} ·
                  {{ formatBytes(attachment.sizeBytes) }}</span
                >
                <small v-if="attachment.error" class="attachment-error">{{
                  attachment.error.message ?? attachment.error
                }}</small>
                <small v-if="attachment.relativePath" class="attachment-path">{{
                  attachment.relativePath
                }}</small>
              </div>
              <StatusBadge :state="attachmentState(attachment)" />
            </div>
          </div>
          <div v-else class="muted">没有附件信息。</div>
        </section>
        <div class="detail-actions">
          <el-button @click="exportMetadata(selectedResource)">
            <template #icon><FileJson2 :size="15" /></template>导出整帖元数据
          </el-button>
          <el-button
            v-if="canRetry(selectedResource)"
            type="primary"
            :loading="retryingResourceIds.has(idOf(selectedResource))"
            @click="retryIncomplete(idOf(selectedResource))"
          >
            <template #icon><RefreshCw :size="15" /></template>补齐未完成文件
          </el-button>
        </div>
      </template>
    </el-drawer>
  </main>
</template>
<style scoped>
.telegram-page {
  max-width: 1580px;
  margin: 0 auto;
  padding: 28px 32px 40px;
  color: #20312e;
}
.telegram-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
  margin-bottom: 22px;
}
.telegram-heading {
  display: flex;
  align-items: center;
  gap: 14px;
}
.telegram-brand-mark {
  display: grid;
  width: 44px;
  height: 44px;
  place-items: center;
  border: 1px solid #d9e6e1;
  border-radius: 13px;
  background: #edf6f2;
  color: #166a59;
}
.telegram-page .page-title {
  margin: 0;
  font-size: 24px;
  font-weight: 650;
  line-height: 1.2;
  letter-spacing: -0.02em;
}
.telegram-page .page-subtitle {
  margin: 6px 0 0;
  color: #71817c;
  font-size: 14px;
}
.saved-sync-button {
  min-height: 40px;
  padding: 0 17px;
  font-weight: 600;
}
.telegram-page-alert {
  margin: 0 0 18px;
}
.telegram-tabs {
  display: flex;
  gap: 4px;
  width: max-content;
  max-width: 100%;
  margin: 0 0 18px;
  padding: 4px;
  border: 1px solid #e2e9e6;
  border-radius: 12px;
  background: #f5f8f7;
}
.telegram-tab {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 38px;
  padding: 0 15px;
  border: 0;
  border-radius: 9px;
  background: transparent;
  color: #63736f;
  font: inherit;
  font-size: 14px;
  cursor: pointer;
  transition:
    background 0.16s ease,
    color 0.16s ease,
    box-shadow 0.16s ease;
}
.telegram-tab:hover {
  color: #166a59;
}
.telegram-tab.active {
  background: #fff;
  color: #166a59;
  box-shadow:
    0 1px 3px #18352d12,
    0 0 0 1px #e2e9e6;
  font-weight: 600;
}
.telegram-tab:focus-visible,
.message-expand:focus-visible,
.message-preview:focus-visible,
.resource-main:focus-visible {
  outline: 3px solid #8bc9b8;
  outline-offset: 2px;
}
.telegram-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 294px;
  align-items: start;
  gap: 18px;
}
.telegram-main-panel,
.watcher-panel {
  border: 1px solid #e4eae8;
  border-radius: 14px;
  background: #fff;
  box-shadow: 0 3px 12px #1c3b3210;
}
.telegram-main-panel {
  min-width: 0;
  padding: 22px;
}
.telegram-section-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
  margin-bottom: 18px;
}
.telegram-section-top .section-title {
  margin: 0;
  color: #233631;
  font-size: 17px;
  font-weight: 650;
}
.telegram-section-top .muted {
  margin: 5px 0 0;
  font-size: 13px;
}
.telegram-search {
  width: min(310px, 42%);
}
.telegram-loading {
  padding: 20px 4px;
}
.telegram-empty {
  min-height: 225px;
  color: #87938f;
}
.telegram-empty strong {
  color: #465752;
  font-size: 15px;
}
.message-stream {
  display: grid;
  gap: 10px;
}
.message-card {
  display: grid;
  grid-template-columns: 24px minmax(0, 1fr);
  gap: 10px;
  padding: 16px 15px;
  border: 1px solid #e8eeec;
  border-radius: 12px;
  background: #fff;
  transition:
    border-color 0.16s ease,
    background 0.16s ease;
}
.message-card:hover {
  border-color: #cbded7;
  background: #fcfefd;
}
.message-expand {
  display: grid;
  width: 24px;
  height: 28px;
  place-items: center;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: #6a7d77;
  cursor: pointer;
}
.message-expand:hover {
  background: #edf6f2;
  color: #166a59;
}
.message-card-content {
  min-width: 0;
}
.message-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 9px 13px;
  color: #798782;
  font-size: 12px;
}
.message-time {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: #465a54;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.message-group {
  padding: 2px 7px;
  border-radius: 5px;
  background: #f0f5f3;
  color: #507066;
}
.message-attachment-count {
  color: #84918d;
}
.message-preview {
  display: -webkit-box;
  width: 100%;
  margin: 9px 0 0;
  padding: 0;
  overflow: hidden;
  border: 0;
  background: transparent;
  color: #354843;
  font: inherit;
  font-size: 14px;
  line-height: 1.65;
  text-align: left;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  cursor: pointer;
}
.message-expanded {
  margin-top: 13px;
  padding-top: 13px;
  border-top: 1px solid #edf1ef;
}
.full-message-text {
  color: #334740;
  font-size: 14px;
  line-height: 1.8;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.full-message-text a,
.source-links a {
  color: #176d5a;
  text-decoration: underline;
  text-decoration-color: #b2d5ca;
  text-underline-offset: 3px;
}
.source-links {
  display: grid;
  gap: 6px;
  margin-top: 11px;
}
.source-links a {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  overflow-wrap: anywhere;
  font-size: 13px;
}
.attachment-list {
  display: grid;
  gap: 7px;
  margin-top: 13px;
}
.attachment-row {
  display: flex;
  align-items: center;
  gap: 11px;
  min-width: 0;
  padding: 10px 11px;
  border: 1px solid #edf1ef;
  border-radius: 9px;
  background: #fafcfb;
}
.attachment-icon {
  display: grid;
  flex: 0 0 30px;
  width: 30px;
  height: 30px;
  place-items: center;
  border-radius: 8px;
  background: #eaf3ef;
  color: #43816f;
}
.attachment-copy {
  display: grid;
  flex: 1;
  min-width: 0;
  gap: 3px;
}
.attachment-copy strong {
  overflow: hidden;
  color: #364b44;
  font-size: 13px;
  font-weight: 550;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.attachment-copy span,
.attachment-copy small {
  color: #86928e;
  font-size: 12px;
  overflow-wrap: anywhere;
}
.attachment-copy .attachment-error {
  color: #b8524b;
}
.attachment-copy .attachment-path {
  color: #77847f;
  font-family: ui-monospace, Consolas, monospace;
}
.no-attachments {
  margin: 12px 0 0;
  font-size: 13px;
}
.list-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: 15px;
  padding-top: 13px;
  border-top: 1px solid #edf1ef;
  font-size: 13px;
}
.list-footer .muted {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.watcher-panel {
  padding: 19px 18px;
}
.watcher-panel-top {
  display: grid;
  grid-template-columns: 36px minmax(0, 1fr) auto;
  align-items: center;
  gap: 10px;
}
.watcher-icon {
  display: grid;
  width: 36px;
  height: 36px;
  place-items: center;
  border-radius: 10px;
  background: #edf6f2;
  color: #166a59;
}
.watcher-panel-top h2 {
  margin: 0;
  color: #30463f;
  font-size: 15px;
  font-weight: 650;
}
.watcher-panel-top p {
  margin: 4px 0 0;
  color: #82908b;
  font-size: 12px;
}
.watcher-prerequisite {
  display: flex;
  gap: 8px;
  margin-top: 17px;
  padding: 10px;
  border: 1px solid #f0e3c7;
  border-radius: 9px;
  background: #fffaf0;
  color: #8b6a26;
  font-size: 12px;
  line-height: 1.55;
}
.watcher-prerequisite svg {
  flex: 0 0 auto;
  margin-top: 1px;
}
.watcher-stats {
  display: grid;
  gap: 13px;
  margin-top: 18px;
}
.watcher-stat {
  display: grid;
  gap: 5px;
}
.watcher-stat > span {
  color: #899590;
  font-size: 11px;
}
.watcher-stat strong {
  color: #4d5d58;
  font-size: 12px;
  font-weight: 550;
  line-height: 1.45;
  overflow-wrap: anywhere;
  font-variant-numeric: tabular-nums;
}
.sync-progress {
  margin-top: 18px;
  padding: 13px 12px;
  border: 1px solid #dcebe5;
  border-radius: 10px;
  background: #f7fbf9;
}
.sync-progress-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  color: #3d5e52;
  font-size: 12px;
  font-weight: 600;
}
.sync-progress-title > span {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.sync-progress p {
  margin: 9px 0 7px;
  color: #465b54;
  font-size: 12px;
}
.sync-progress small {
  display: block;
  margin-top: 7px;
  color: #7c8c86;
  font-size: 11px;
  line-height: 1.5;
}
.watcher-error {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  margin-top: 15px;
  color: #af5149;
  font-size: 12px;
  line-height: 1.55;
}
.watcher-error svg {
  flex: 0 0 auto;
  margin-top: 1px;
}
.watcher-note {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  margin-top: 18px;
  padding-top: 14px;
  border-top: 1px solid #edf1ef;
  color: #81908a;
  font-size: 11px;
  line-height: 1.55;
}
.watcher-note svg {
  flex: 0 0 auto;
  color: #6e9b8c;
}
.links-limit {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: #688078;
  font-size: 12px;
  white-space: nowrap;
}
.link-download-card {
  padding: 20px;
  border: 1px solid #e7eeeb;
  border-radius: 12px;
  background: #fcfefd;
}
.input-label {
  display: block;
  margin-bottom: 9px;
  color: #445852;
  font-size: 13px;
  font-weight: 600;
}
.link-input-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-top: 14px;
  color: #7d8b86;
  font-size: 12px;
}
.validation-note {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin-top: 13px;
  padding: 10px 11px;
  border-radius: 8px;
  font-size: 12px;
  line-height: 1.55;
}
.validation-note svg {
  flex: 0 0 auto;
  margin-top: 1px;
}
.validation-note ul {
  margin: 6px 0 3px;
  padding-left: 18px;
  color: #965c54;
  overflow-wrap: anywhere;
}
.validation-error {
  border: 1px solid #f1d8d4;
  background: #fff8f7;
  color: #a14e45;
}
.validation-ok {
  border: 1px solid #d9ebe3;
  background: #f3faf6;
  color: #4b7b69;
}
.link-scope-note {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  margin-top: 16px;
  color: #82918b;
  font-size: 12px;
  line-height: 1.6;
}
.link-scope-note svg {
  flex: 0 0 auto;
  margin-top: 1px;
  color: #679787;
}
.resource-heading {
  margin-bottom: 17px;
}
.resource-heading > .el-button {
  flex: 0 0 auto;
}
.resource-filters {
  display: flex;
  align-items: center;
  gap: 11px;
  margin-bottom: 14px;
}
.resource-filters .telegram-search {
  width: min(360px, 55%);
}
.resource-filters .el-select {
  width: 155px;
}
.filter-caption {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  margin-left: auto;
  color: #929d99;
  font-size: 11px;
  white-space: nowrap;
}
.resource-list {
  display: grid;
  gap: 9px;
}
.resource-card {
  padding: 0 13px 10px;
  border: 1px solid #e8eeec;
  border-radius: 11px;
}
.resource-main {
  display: grid;
  grid-template-columns: 34px minmax(0, 1fr) auto 17px;
  align-items: center;
  gap: 11px;
  width: 100%;
  min-height: 82px;
  padding: 10px 0;
  border: 0;
  background: transparent;
  color: inherit;
  text-align: left;
  cursor: pointer;
}
.resource-icon {
  display: grid;
  width: 34px;
  height: 34px;
  place-items: center;
  border-radius: 9px;
  background: #edf5f2;
  color: #588373;
}
.resource-copy {
  display: grid;
  min-width: 0;
  gap: 4px;
}
.resource-copy strong {
  overflow: hidden;
  color: #344941;
  font-size: 13px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.resource-subtitle,
.resource-date {
  overflow: hidden;
  color: #84918c;
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.resource-status {
  display: grid;
  justify-items: end;
  gap: 4px;
}
.resource-status > span {
  color: #ad5d54;
  font-size: 11px;
}
.resource-chevron {
  color: #9aa7a2;
}
.resource-actions {
  display: flex;
  gap: 5px;
  padding-left: 45px;
}
.telegram-dialog .confirm-content {
  display: flex;
  align-items: flex-start;
  gap: 14px;
  padding: 3px 0 8px;
}
.confirm-icon {
  display: grid;
  flex: 0 0 40px;
  width: 40px;
  height: 40px;
  place-items: center;
  border-radius: 11px;
  background: #edf6f2;
  color: #166a59;
}
.confirm-content strong {
  color: #344941;
  font-size: 14px;
}
.confirm-content p {
  margin: 8px 0 0;
  color: #687872;
  font-size: 13px;
  line-height: 1.65;
}
.resource-detail-heading {
  padding-bottom: 18px;
  border-bottom: 1px solid #edf1ef;
}
.resource-detail-eyebrow {
  color: #82918b;
  font-size: 12px;
}
.resource-detail-heading h2 {
  margin: 7px 0 9px;
  color: #344a42;
  font-size: 19px;
  font-weight: 650;
  overflow-wrap: anywhere;
}
.detail-badges {
  display: flex;
  align-items: center;
  gap: 9px;
  color: #81908a;
  font-size: 12px;
}
.detail-summary-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  padding: 17px 0;
  border-bottom: 1px solid #edf1ef;
}
.detail-summary-grid > div {
  display: grid;
  gap: 4px;
  min-width: 0;
}
.detail-summary-grid span {
  color: #899590;
  font-size: 11px;
}
.detail-summary-grid strong {
  color: #4b5e56;
  font-size: 13px;
  overflow-wrap: anywhere;
}
.detail-section {
  padding: 17px 0;
  border-bottom: 1px solid #edf1ef;
}
.detail-section h3 {
  display: flex;
  align-items: center;
  gap: 7px;
  margin: 0 0 10px;
  color: #43574f;
  font-size: 13px;
  font-weight: 650;
}
.detail-section h3 span {
  color: #899590;
  font-size: 11px;
  font-weight: 500;
}
.detail-message {
  margin: 0;
  color: #42554e;
  font-size: 13px;
  line-height: 1.75;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.detail-section .attachment-list {
  margin-top: 0;
}
.detail-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  padding-top: 17px;
}
.spin {
  animation: telegram-spin 1.4s linear infinite;
}
@keyframes telegram-spin {
  to {
    transform: rotate(360deg);
  }
}

@media (max-width: 1180px) {
  .telegram-page {
    padding: 24px 22px 34px;
  }
  .telegram-layout {
    grid-template-columns: minmax(0, 1fr) 270px;
    gap: 14px;
  }
  .telegram-main-panel {
    padding: 18px;
  }
  .watcher-panel {
    padding: 16px 14px;
  }
}
@media (max-width: 1023px) {
  .telegram-page {
    min-width: 980px;
    padding-right: 18px;
    padding-left: 18px;
  }
}
</style>
