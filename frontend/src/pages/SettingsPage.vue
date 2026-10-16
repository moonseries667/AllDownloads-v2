<script setup lang="ts">
import { computed, ref, onMounted, watch } from "vue";
import {
  FolderOpen,
  Cloud,
  MonitorPlay,
  Send,
  CloudDownload as FolderCloud,
  RefreshCw,
  FlaskConical,
  ShieldCheck,
  Save,
} from "lucide-vue-next";
import { ElMessageBox } from "element-plus";
import {
  api,
  dataRevision,
  isDemo,
  notifyError,
  notifySuccess,
} from "../api/client";
import { platformName, formatDate } from "../format";
import StatusBadge from "../components/StatusBadge.vue";
import NativeDirectoryField from "../components/NativeDirectoryField.vue";
import DriveFolderPicker from "../components/DriveFolderPicker.vue";
import { archiveSettingsApi, errorMessage, type StorageSettings, type SettingsValidation } from "../api/archive-settings";
import { serviceLabels, mountLabels } from "../components/runtimePresentation";
import { accountsApi, type Account, type AccountPlatform } from "../api/accounts";
import AccountLoginDialog from "../components/AccountLoginDialog.vue";
const settings = ref<any>({}),
  form = ref<any>({}),
  accounts = ref<Account[]>([]),
  health = ref<any>({}),
  validation = ref<SettingsValidation | null>(null),
  saving = ref(false),
  scenario = ref("normal"),
  switching = ref(false),
  auth = ref<any>(null),
  authOpen = ref(false);
const authPlatform = ref<AccountPlatform | null>(null), checkingAccount = ref("");
const validating = ref(false), nativeSelecting = ref(false), draftRevision = ref(""), driveAccountId = ref("");
const storageError = ref(""), versionConflict = ref(false);
const accountError = ref(""), settingsError = ref("");
const driveAccounts = computed(() => accounts.value.filter(account => account.platform === "GOOGLE_DRIVE"));
const pickerUnavailable = computed(() => isDemo ? "演示模式不打开系统窗口。" : "运行主机尚未提供可用的原生目录选择能力，请确认使用同机浏览器与可交互桌面。");
function setDraft(value: StorageSettings) {
  form.value = JSON.parse(JSON.stringify(value)); draftRevision.value = value.revision;
  driveAccountId.value = value.archiveDestination?.accountId || driveAccounts.value[0]?.id || "";
  validation.value = null;
  storageError.value = ""; versionConflict.value = false;
}
function storageFailure(cause: unknown) {
  storageError.value = cause instanceof Error ? cause.message : "存储设置未能保存，请重试。";
  versionConflict.value = (cause as { code?: string })?.code === "VERSION_CHANGED";
  notifyError(cause);
}
async function reloadSettings() {
  try { settings.value = await api.get("/settings"); setDraft(settings.value); }
  catch (cause) { storageFailure(cause); }
}
async function selectDirectory(signal: AbortSignal) {
  const result = await archiveSettingsApi.selectDirectory(signal);
  if (result.status === "SELECTED" && result.target) {
    form.value.defaultTarget = result.target; form.value.downloadRoot = result.path;
  }
  return result;
}
function serviceState(id: string) {
  return serviceLabels[health.value.runtime?.services?.find((service: { id: string }) => service.id.toLowerCase() === id)?.state] || "未知";
}
const scenarios = [
  {
    value: "normal",
    label: "默认示例",
    description:
      "包含未下载、下载中、来源失败、附件部分完成、上传待登录与云端待核对。",
  },
  {
    value: "scanned",
    label: "首次扫描已完成",
    description: "验证 Telegram 收藏监控开关与游标显示。",
  },
  {
    value: "empty",
    label: "空内容库",
    description: "验证首次使用、空列表与添加入口。",
  },
  {
    value: "disk",
    label: "本机空间不足",
    description: "创建下载预览正常，提交订单会显示空间不足。",
  },
  {
    value: "runner",
    label: "执行程序不可用",
    description: "保留浏览与选择，提交操作显示 Go 不可用。",
  },
  {
    value: "login",
    label: "平台待登录",
    description: "操作提示账号需要登录，页面保留已有内容。",
  },
  {
    value: "offline",
    label: "连接中断",
    description: "顶部显示状态可能过期，保留缓存内容，阻止写操作。",
  },
  {
    value: "version",
    label: "预览版本冲突",
    description: "下载提交时提示文件版本已变化，要求重新预览。",
  },
];
async function load() {
  try {
    const [s, a, h] = await Promise.allSettled([
      api.get("/settings"),
      accountsApi.list(),
      api.get("/health"),
    ]);
    if (s.status === "fulfilled") {
      settings.value = s.value;
      settingsError.value = "";
      if (!Object.keys(form.value).length) setDraft(s.value);
    } else { settingsError.value = s.reason.message; notifyError(s.reason); }
    if (a.status === "fulfilled") {
      accounts.value = a.value.items;
      accountError.value = "";
      if (!driveAccountId.value && driveAccounts.value.length === 1) driveAccountId.value = driveAccounts.value[0].id;
    }
    else { accountError.value = a.reason?.code === "NOT_FOUND" ? "当前 Java 版本尚未提供账号状态接口，登录状态无法读取。" : a.reason.message; notifyError(a.reason); }
    if (h.status === "fulfilled") health.value = h.value;
    else { health.value = {}; notifyError(h.reason); }
    if (isDemo) scenario.value = (await api.get("/__demo/status")).scenario;
  } catch (e) {
    notifyError(e);
  }
}
async function validate() {
  storageError.value = ""; versionConflict.value = false;
  validating.value = true;
  try {
    if (!form.value.defaultTarget) throw Error("请先读取已验证的本机下载目录。");
    validation.value = await archiveSettingsApi.validate(form.value.defaultTarget, form.value.archiveDestination ?? null);
    return validation.value;
  } catch (e) {
    storageFailure(e);
    return null;
  } finally { validating.value = false; }
}
async function save() {
  saving.value = true;
  try {
    const result = await validate();
    if (!result || result.status !== "VALID") return;
    settings.value = await archiveSettingsApi.save(result.id, draftRevision.value);
    setDraft(settings.value);
    notifySuccess("存储设置已保存。已有订单继续使用原目标，文件保留原位。");
  } catch (e) {
    storageFailure(e);
  } finally {
    saving.value = false;
  }
}
async function changeScenario(value: string) {
  switching.value = true;
  try {
    await ElMessageBox.confirm(
      "将重置本浏览器中的虚构演示记录，以加载所选场景；不影响真实文件或账号。",
      "切换验收场景",
      { confirmButtonText: "加载场景", cancelButtonText: "取消" },
    );
    await api.post("/__demo/scenario", { scenario: value });
    form.value = {};
    validation.value = null;
    await load();
    notifySuccess("验收场景已加载");
  } catch (e) {
    if (e !== "cancel" && e !== "close") notifyError(e);
    await load();
  } finally {
    switching.value = false;
  }
}
async function check(id: string) {
  if (checkingAccount.value) return;
  checkingAccount.value = id;
  try {
    await accountsApi.check(id);
    notifySuccess(isDemo ? "已读取账号示例状态；未连接真实平台。" : "账号检查已受理，状态以执行程序返回的事实为准。");
  } catch (e) {
    notifyError(e);
  } finally { checkingAccount.value = ""; }
}
async function login(platform: AccountPlatform) {
  if (!isDemo) { authPlatform.value = platform; authOpen.value = true; return; }
  try {
    auth.value = await api.post("/auth-sessions", { platform });
    authOpen.value = true;
  } catch (e) {
    notifyError(e);
  }
}
async function cancelLogin() {
  if (auth.value) {
    try {
      await api.delete("/auth-sessions/" + auth.value.authSessionId);
    } catch (e) {
      notifyError(e);
    }
  }
  authOpen.value = false;
  auth.value = null;
}
onMounted(load);
watch(dataRevision, load);
watch(form, () => (validation.value = null), { deep: true });
</script>
<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-eyebrow">工作空间偏好</div>
        <h1 class="page-title">设置与账号</h1>
        <p class="page-subtitle">
          一个下载根目录，独立的云端目标，以及各平台的连接状态。
        </p>
      </div>
    </div>
    <div class="settings-layout">
      <div class="settings-main">
        <section class="panel settings-section">
          <div class="section-heading">
            <h2><FolderOpen :size="21" />存储位置</h2>
            <span class="muted">目录版本 {{ settings.storageRevision }}</span>
          </div>
          <div v-if="settingsError" class="mb16" role="alert"><p class="text-danger">存储设置读取失败：{{ settingsError }}</p><el-button @click="load">重新读取</el-button></div>
          <el-form label-position="top">
            <el-form-item label="统一本机下载根目录">
              <NativeDirectoryField :model-value="form.downloadRoot || ''" :select-directory="selectDirectory" :disabled="saving || validating || !form.defaultTarget" @busy="nativeSelecting = $event" />
              <p v-if="settings.directoryPicker && !settings.directoryPicker.available" class="field-help">{{ pickerUnavailable }}</p>
            </el-form-item>
            <el-form-item label="Goole Drive上传目录">
              <el-select v-if="driveAccounts.length > 1" v-model="driveAccountId" aria-label="Google Drive账号" :disabled="saving || validating" @change="form.archiveDestination = null"><el-option v-for="account in driveAccounts" :key="account.id" :value="account.id" :label="account.label" /></el-select>
              <DriveFolderPicker :model-value="form.archiveDestination ?? null" :account-id="driveAccountId" :disabled="saving || validating || nativeSelecting" @update:model-value="form.archiveDestination = $event" />
              <p class="field-help">选择目录后还需校验并保存；已排队上传继续使用原来的 Google Drive 目标。</p>
              <p v-if="!driveAccounts.length" role="status">尚无 Google Drive 账号，请先完成账号配置。</p>
            </el-form-item>
            <div class="form-two">
              <el-form-item label="同时下载数量"><span>{{ settings.downloadConcurrency ?? '由运行配置管理' }}</span></el-form-item>
              <el-form-item label="同时上传数量"><span>{{ settings.uploadConcurrency ?? '由运行配置管理' }}</span></el-form-item>
            </div>
            <p class="muted">显示时区：Asia/Shanghai。全局默认目录与下载清单中的本批目录分别保存。</p>
            <p v-if="validating" role="status">正在校验本机目录与 Google Drive 目标…</p>
            <div v-if="storageError" role="alert" class="mb16"><p class="text-danger">{{ storageError }}</p><el-button v-if="versionConflict" @click="reloadSettings">载入最新设置并重选目录</el-button></div>
            <div v-if="validation" class="mb16">
              <el-alert :title="validation.status === 'VALID' ? (isDemo ? '隔离演示校验通过' : '目录校验通过') : '设置暂时无法应用'" :type="validation.status === 'VALID' ? 'success' : 'error'" :closable="false" show-icon />
              <p v-if="validation.error" class="text-danger">{{ errorMessage(validation.error, '目录已失效或权限发生变化，请重新选择。') }}</p>
            </div>
            <div class="toolbar">
              <el-button :icon="ShieldCheck" :loading="validating" :disabled="saving || nativeSelecting || !form.defaultTarget" @click="validate"
                >校验设置</el-button
              ><el-button
                type="primary"
                :icon="Save"
                :loading="saving"
                :disabled="validating || nativeSelecting || !form.defaultTarget"
                @click="save"
                >保存设置</el-button
              >
            </div></el-form
          >
        </section>
        <section class="panel settings-section">
          <div class="section-heading">
            <h2><Cloud :size="21" />平台账号</h2>
            <span class="muted">不展示或保存已有凭据</span>
          </div>
          <div v-if="accountError" role="alert"><p class="text-danger">{{ accountError }}</p><el-button @click="load">重新读取账号</el-button></div>
          <p v-else-if="!accounts.length" class="muted">尚无可读取的平台账号。</p>
            <p v-if="!isDemo" class="muted">Bilibili 只使用一个主账号读取与下载。检查仅核对身份与读权限，不扫描或下载。</p>
          <div
            v-for="account in accounts"
            :key="account.id"
            class="account-row"
          >
            <span class="account-icon"
              ><component
                :is="
                  account.platform === 'BILIBILI'
                    ? MonitorPlay
                    : account.platform === 'TELEGRAM'
                      ? Send
                      : account.platform === 'QUARK'
                        ? FolderCloud
                        : Cloud
                "
                :size="22"
            /></span>
            <div class="account-info">
              <strong>{{ platformName[account.platform] }}</strong
              ><small
                >{{ account.label }} ·
                {{ formatDate(account.lastCheckedAt) }}</small
              >
            </div>
            <StatusBadge :state="account.state" /><el-button
              link
              :loading="checkingAccount === account.id"
              :disabled="!!checkingAccount"
              @click="check(account.id)"
              >检查</el-button
            ><el-button type="primary" plain @click="login(account.platform)">{{
              account.state === "AUTH_REQUIRED" ? "重新登录" : "登录管理"
            }}</el-button>
          </div>
        </section>
      </div>
      <aside class="settings-side">
        <section class="panel settings-section">
          <h2>运行状态</h2>
          <div class="health-row">
            <span>Java 业务接口</span
            ><strong>{{
              serviceState('java')
            }}</strong>
          </div>
          <div class="health-row">
            <span>Go 执行程序</span
            ><strong>{{
              serviceState('go')
            }}</strong>
          </div>
          <div class="health-row">
            <span>网盘挂载</span
            ><strong>{{
              mountLabels[health.runtime?.mount?.state || 'UNKNOWN'] || '挂载状态未知'
            }}</strong>
          </div>
          <p class="muted">
            观测时间：{{ formatDate(health.runtime?.observedAt) }}。挂载与归档核验独立。
          </p>
          <p v-if="isDemo" class="muted">隔离演示，不表示真实服务或网盘状态。</p>
        </section>
        <section v-if="isDemo" class="panel settings-section demo-controls">
          <h2><FlaskConical :size="21" />前端验收场景</h2>
          <p class="muted">仅影响本浏览器的演示数据。</p>
          <el-select
            :model-value="scenario"
            @change="changeScenario"
            :disabled="switching"
            aria-label="前端验收场景"
            ><el-option
              v-for="s in scenarios"
              :key="s.value"
              :label="s.label"
              :value="s.value"
          /></el-select>
          <p>{{ scenarios.find((s) => s.value === scenario)?.description }}</p>
          <el-button
            :icon="RefreshCw"
            @click="changeScenario('normal')"
            :disabled="switching"
            >恢复默认示例</el-button
          >
        </section>
      </aside>
    </div>
    <el-dialog
      v-if="isDemo"
      v-model="authOpen"
      title="账号登录流程"
      width="530px"
      :close-on-click-modal="false"
      :before-close="cancelLogin"
      ><div class="auth-placeholder">
        <ShieldCheck :size="38" />
        <h3>前端演示登录入口</h3>
        <p>{{ auth?.message || "等待官方登录步骤" }}</p>
      </div>
      <el-alert
        title="本轮不会生成伪二维码，也不需要输入真实手机号、验证码或密码。"
        type="info"
        :closable="false"
      />
      <p class="muted">
        正式接口将按平台返回二维码、授权地址或必要输入步骤。演示会话到期时间：{{
          formatDate(auth?.expiresAt)
        }}
      </p>
      <template #footer
        ><el-button @click="cancelLogin">取消登录流程</el-button></template
      ></el-dialog
    >
    <AccountLoginDialog v-else v-model="authOpen" :platform="authPlatform" @changed="load" />
  </div>
</template>
