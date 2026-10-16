<script setup lang="ts">
import { computed, onActivated, onBeforeUnmount, onDeactivated, ref, watch } from "vue";
import QRCode from "qrcode";
import { ExternalLink, RefreshCw, ShieldCheck } from "lucide-vue-next";
import { accountIds, accountsApi, officialAuthUrl, type AccountPlatform, type AuthSession } from "../api/accounts";
import { formatDate, platformName } from "../format";
const props = defineProps<{ modelValue: boolean; platform: AccountPlatform | null }>();
const emit = defineEmits<{ "update:modelValue": [value: boolean]; changed: [] }>();
const session = ref<AuthSession | null>(null), qrImage = ref("");
const loading = ref(false), cancelling = ref(false), error = ref(""), pollingStopped = ref(false);
const officialUrl = computed(() => session.value ? officialAuthUrl(session.value) : null);
const terminal = computed(() => !!session.value && session.value.state !== "PENDING");
const stateLabels = { PENDING: "等待授权", SUCCEEDED: "授权流程已完成", CANCELLED: "登录流程已取消", EXPIRED: "登录流程已过期", NEEDS_ACTION: "登录需要处理" };
let generation = 0, attempts = 0, lastStepAt = 0;
let timer: ReturnType<typeof setTimeout> | undefined;
function stop() { generation++; clearTimeout(timer); loading.value = false; }
async function apply(value: AuthSession, run: number) {
  if (run !== generation || !props.modelValue) return;
  if (value.platform !== props.platform || value.accountId !== accountIds[value.platform]) throw Error("登录流程与当前平台账号不一致，请重新读取。");
  if (!Number.isFinite(Date.parse(value.expiresAt))) throw Error("登录流程缺少有效的到期时间，请重新读取。");
  if (session.value && value.authSessionId !== session.value.authSessionId) throw Error("登录流程身份变化，请关闭后重新开始。");
  session.value = value; error.value = ""; qrImage.value = "";
  if (value.nextAction === "SCAN_QR" && value.state === "PENDING") {
    const url = officialAuthUrl(value);
    if (!url) throw Error("未收到当前平台的官方二维码地址，请重新读取登录状态。");
    const image = await QRCode.toDataURL(url, { width: 240, margin: 2, errorCorrectionLevel: "M" });
    if (run === generation) qrImage.value = image;
  }
  if (value.nextAction === "OPEN_URL" && !officialAuthUrl(value)) throw Error("未收到当前平台的官方授权地址，请重新读取登录状态。");
  if (value.state !== "PENDING") emit("changed");
}
function schedule(run: number) {
  clearTimeout(timer);
  if (run !== generation || !props.modelValue || terminal.value || error.value) return;
  if (attempts >= 90 || (session.value && Date.parse(session.value.expiresAt) <= Date.now())) { pollingStopped.value = true; return; }
  timer = setTimeout(() => void refresh(true), document.hidden ? 15000 : 2000);
}
async function refresh(automatic = false) {
  const current = session.value;
  if (!current || loading.value || cancelling.value) return;
  const run = generation;
  if (!automatic) { attempts = 0; pollingStopped.value = false; }
  if (automatic && document.hidden) { schedule(run); return; }
  loading.value = true;
  try {
    attempts++;
    await apply(await accountsApi.session(current.authSessionId), run);
    const latest = session.value;
    // A pending runner task must finish before another AUTH_STEP is queued.
    if (automatic && latest?.state === "PENDING" && ["SCAN_QR", "OPEN_URL"].includes(latest.nextAction)
      && Date.parse(latest.expiresAt) > Date.now() && Date.now() - lastStepAt >= 4000) {
      const task = latest.taskId ? await accountsApi.task(latest.taskId) : null;
      if (run === generation && (!task || task.status === "SUCCEEDED")) {
        lastStepAt = Date.now();
        await apply(await accountsApi.poll(latest.authSessionId), run);
      }
    }
  } catch (cause) { if (run === generation) error.value = (cause as Error).message; }
  finally { if (run === generation) { loading.value = false; schedule(run); } }
}
async function begin() {
  stop();
  if (!props.modelValue || !props.platform) return;
  const run = generation;
  session.value = null; error.value = ""; qrImage.value = ""; pollingStopped.value = false;
  attempts = 0; lastStepAt = 0; loading.value = true;
  try { await apply(await accountsApi.begin(props.platform), run); }
  catch (cause) { if (run === generation) error.value = (cause as Error).message; }
  finally { if (run === generation) { loading.value = false; schedule(run); } }
}
async function close() {
  if (cancelling.value || loading.value) return;
  clearTimeout(timer);
  cancelling.value = true;
  try {
    if (session.value?.state === "PENDING") {
      await apply(await accountsApi.cancel(session.value), generation);
      emit("changed");
    }
    stop(); emit("update:modelValue", false);
  } catch (cause) { error.value = "取消登录未能确认：" + (cause as Error).message + "。请重新读取后重试。"; }
  finally { cancelling.value = false; }
}
watch(() => [props.modelValue, props.platform], () => { if (props.modelValue) void begin(); else stop(); }, { immediate: true });
onBeforeUnmount(stop); onDeactivated(stop);
onActivated(() => { if (props.modelValue && session.value?.state === "PENDING") void refresh(); });
</script>
<template>
  <el-dialog :model-value="modelValue" :title="(platform ? platformName[platform] : '账号') + ' 登录'" width="560px" :close-on-click-modal="false" :close-on-press-escape="!loading && !cancelling" :show-close="!loading && !cancelling" :before-close="close">
    <div class="account-auth" :aria-busy="loading">
      <ShieldCheck :size="30" aria-hidden="true" />
      <h3>{{ session ? stateLabels[session.state] : loading ? '正在建立登录流程…' : '登录流程尚未建立' }}</h3>
      <p v-if="error" class="text-danger" role="alert">{{ error }}</p>
      <template v-if="session">
        <p v-if="session.message">{{ session.message }}</p>
        <p v-if="session.state === 'PENDING' && session.nextAction === 'WAIT'" role="status">正在等待执行程序返回官方授权步骤。</p>
        <img v-if="qrImage && session.state === 'PENDING' && !pollingStopped" :src="qrImage" class="auth-qr" :alt="'使用 ' + platformName[session.platform] + ' 官方客户端扫码登录'" />
        <a v-if="session.nextAction === 'OPEN_URL' && officialUrl && session.state === 'PENDING'" class="official-auth-link" :href="officialUrl" target="_blank" rel="noopener noreferrer">打开官方授权页 <ExternalLink :size="15" aria-hidden="true" /></a>
        <p v-if="session.nextAction === 'LOCAL_SETUP'" class="field-help">请在 Y7000P 的受控本地入口完成新会话配置，再回到平台账号点击「检查」。</p>
        <p class="muted">到期时间：{{ formatDate(session.expiresAt) }}</p>
        <p v-if="pollingStopped" role="status">自动读取已停止，请重新读取服务状态。过期流程需关闭后重新开始。</p>
      </template>
      <p class="field-help">仅在 Y7000P 完成官方授权。本页不接收密码、验证码、Cookie 或令牌；登录不会自动创建下载或上传。</p>
    </div>
    <template #footer>
      <el-button :icon="RefreshCw" :loading="loading" :disabled="cancelling" @click="session ? refresh() : begin()">重新读取</el-button>
      <el-button :loading="cancelling" :disabled="loading" @click="close">{{ !session || terminal ? '关闭' : '取消登录流程' }}</el-button>
    </template>
  </el-dialog>
</template>
<style scoped>
.account-auth { display: flex; flex-direction: column; align-items: center; text-align: center; gap: 12px; }
.account-auth h3, .account-auth p { margin: 0; line-height: 1.7; overflow-wrap: anywhere; }
.auth-qr { width: 240px; height: 240px; background: white; border: 1px solid var(--line); }
.official-auth-link { display: inline-flex; align-items: center; gap: 8px; padding: 10px 16px; border: 1px solid var(--primary); border-radius: 6px; }
.official-auth-link:focus-visible { outline: 2px solid var(--primary); outline-offset: 3px; }
</style>
