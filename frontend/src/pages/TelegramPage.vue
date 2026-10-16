<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { ArrowDownToLine, ChevronDown, ChevronRight, CircleAlert, Clock3, FileText, Link2, LoaderCircle, MessageSquareText, RefreshCw, Search, ShieldCheck, Video } from "lucide-vue-next";
import { useRoute } from "vue-router";
import StatusBadge from "../components/StatusBadge.vue";
import { api, dataRevision, isDemo } from "../api/client";
import { accountIds } from "../api/accounts";
import { createSavedMessagesApi } from "../api/telegram";
import { notifyError, notifySuccess } from "../notifications/notify";
import { formatBytes, formatDate } from "../format";
const route = useRoute();
type RecordData = Record<string, any>;
type TextSegment = { text: string; href?: string };
const savedMessages = createSavedMessagesApi(api);
const accountId = isDemo ? "tg-main" : accountIds.TELEGRAM;
const messages = ref<RecordData[]>([]);
const watcher = ref<RecordData | null>(null);
const activeTask = ref<RecordData | null>(null);
const totalMessages = ref<number | null>(null);
const nextCursor = ref<string | null>(null);
const expandedMessages = ref(new Set<string>());
const messageDetails = ref<Record<string, RecordData>>({});
const detailLoading = ref(new Set<string>());
const pageError = ref("");
const loading = ref(false);
const taskLoading = ref(false);
const watcherSaving = ref(false);
const syncSubmitting = ref(false);
const retrySubmitting = ref(false);
const cancelling = ref(false);
const messageQuery = ref("");
const showSavedSyncDialog = ref(false);
let requestSequence = 0;
let filterTimer: ReturnType<typeof setTimeout> | undefined;
const scanState = computed<RecordData>(() => watcher.value?.scanState ?? {});
const initialScanCompleted = computed(() => scanState.value.initialScanCompleted === true);
const watcherEnabled = computed(() => watcher.value?.enabled === true);
const syncTask = computed<RecordData | null>(() => activeTask.value);
function attachmentsOf(message: RecordData) { return messageDetails.value[idOf(message)]?.attachments ?? message.attachments ?? []; }
function sourceLinksOf(message: RecordData) { return messageDetails.value[idOf(message)]?.sourceLinks ?? message.sourceLinks ?? []; }
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
  if (value === "VIDEO" || value.startsWith("video/")) return "视频";
  if (value === "AUDIO" || value.startsWith("audio/")) return "音频";
  if (value === "PHOTO" || value.startsWith("image/")) return "图片";
  return value === "DOCUMENT" ? "文件" : value === "UNKNOWN" ? "类型未知" : value;
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

async function loadData(append = false) {
  const sequence = ++requestSequence;
  loading.value = true;
  pageError.value = "";
  try {
    const [watcherData, page] = await Promise.all([
      api.get<RecordData>("/telegram/watcher?accountId=" + encodeURIComponent(accountId)),
      savedMessages.list(messageQuery.value, append ? nextCursor.value : null),
    ]);
    if (sequence !== requestSequence) return;
    watcher.value = watcherData;
    messages.value = append ? [...new Map([...messages.value, ...page.items].map(m => [idOf(m), m])).values()] : page.items;
    totalMessages.value = page.total;
    nextCursor.value = page.nextCursor;
    // List refreshes carry current attachment state; stale detail must not mask it.
    messageDetails.value = {};
    await loadCurrentTask(watcherData, sequence);
  } catch (error) {
    if (sequence !== requestSequence) return;
    pageError.value = error instanceof Error ? error.message : "页面信息读取失败，请重试。";
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
  clearTimeout(filterTimer);
  requestSequence++;
  filterTimer = setTimeout(() => void loadData(), 280);
}
watch(dataRevision, () => void loadData());
watch(() => route.query.q, value => {
  if (route.path === "/telegram") messageQuery.value = typeof value === "string" ? value : "";
}, { immediate: true });
watch(messageQuery, scheduleLoad);
onMounted(() => void loadData());
onUnmounted(() => { clearTimeout(filterTimer); requestSequence++; });
function isExpanded(message: RecordData) { return expandedMessages.value.has(idOf(message)); }
async function toggleMessage(message: RecordData) {
  const id = idOf(message);
  const next = new Set(expandedMessages.value);
  if (next.has(id)) { next.delete(id); expandedMessages.value = next; return; }
  next.add(id);
  expandedMessages.value = next;
  if (messageDetails.value[id] || detailLoading.value.has(id)) return;
  detailLoading.value = new Set(detailLoading.value).add(id);
  try {
    const detail = await savedMessages.detail(id);
    messageDetails.value = { ...messageDetails.value, [id]: detail };
  } catch (error) { notifyError(error); }
  finally { const pending = new Set(detailLoading.value); pending.delete(id); detailLoading.value = pending; }
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
    await api.patch("/telegram/watcher", { accountId, enabled: nextEnabled }, watcher.value?.revision);
    notifySuccess(nextEnabled ? "收藏监控已启用。" : "收藏监控已暂停。");
  } catch (error) {
    notifyError(error);
  } finally {
    watcherSaving.value = false;
  }
}

async function retryIncomplete() {
  retrySubmitting.value = true;
  try {
    await api.post("/telegram/retry-incomplete", { accountId });
    notifySuccess("收藏夹未完成附件的补齐任务已受理，等待执行。");
  } catch (error) { notifyError(error); }
  finally { retrySubmitting.value = false; }
}
async function cancelSync() {
  if (!syncTask.value?.allowedActions?.includes("CANCEL")) return;
  cancelling.value = true;
  try {
    await api.post("/tasks/" + encodeURIComponent(syncTask.value.id) + "/actions", { action: "CANCEL" }, syncTask.value.revision);
    notifySuccess("已请求取消，等待执行程序确认。");
  } catch (error) { notifyError(error); }
  finally { cancelling.value = false; }
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
          <h1 class="page-title">收藏夹</h1>
          <p class="page-subtitle">
            增量下载含音视频的收藏；同一消息或媒体组的照片与其他附件一并保存。
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

    <div class="telegram-layout">
      <section class="telegram-main-panel">
          <div class="telegram-section-top">
            <div>
              <h2 class="section-title">收藏内容</h2>
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
            <strong>没有符合条件的收藏内容</strong>
            <span
              >打开页面不会访问
              Telegram；仅含文字、照片或普通文件的内容会被跳过。</span
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
                    >{{ attachmentsOf(message).length }} 个附件</span
                  >
                  <StatusBadge
                    v-if="messageDownloadState(message)"
                    :state="messageDownloadState(message)"
                  />
                </div>
                <button v-if="!isExpanded(message)" class="message-preview" :aria-expanded="false" @click="toggleMessage(message)">
                  {{ message.text || "此消息没有文字内容" }}
                </button>
                <div v-else class="message-expanded">
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
                  <small v-if="detailLoading.has(idOf(message))" class="muted" role="status">正在读取最新详情…</small>
                  <div v-if="sourceLinksOf(message).length" class="source-links">
                    <a
                      v-for="(link, index) in sourceLinksOf(message)"
                      :key="idOf(message) + '-link-' + index"
                      :href="link"
                      target="_blank"
                      rel="noreferrer noopener"
                    >
                      <Link2 :size="13" />{{ link }}
                    </a>
                  </div>
                  <div
                    v-if="attachmentsOf(message).length"
                    class="attachment-list"
                  >
                    <div
                      v-for="attachment in attachmentsOf(message)"
                      :key="idOf(attachment)"
                      class="attachment-row"
                    >
                      <div class="attachment-icon" aria-hidden="true">
                        <Video
                          v-if="
                            attachment.mediaType === 'VIDEO' || String(attachment.mediaType ?? '').startsWith(
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
                        <small v-if="attachment.error?.message" class="attachment-error">{{ attachment.error.message }}</small>
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
            <el-button v-if="nextCursor" text type="primary" :loading="loading" @click="loadData(true)">加载更多</el-button>
            <span v-if="loading" class="muted"
              ><LoaderCircle class="spin" :size="15" />正在更新</span
            >
          </footer>
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
        <el-button v-if="syncTask?.allowedActions?.includes('CANCEL')" :loading="cancelling" @click="cancelSync">取消同步任务</el-button>
        <el-button :loading="retrySubmitting" @click="retryIncomplete()">补齐未完成附件</el-button>
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
            开始，仅下载包含音频或视频的消息或媒体组；符合条件的同组照片及其他实际附件也会一起保存。纯文字、照片或普通文件不会下载。
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
  margin-top: 9px;
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
    min-width: 0;
    padding-right: 18px;
    padding-left: 18px;
  }
}
.watcher-panel > .el-button { margin: 14px 8px 0 0; }
@media (prefers-reduced-motion: reduce) { .spin { animation: none; } }
</style>
