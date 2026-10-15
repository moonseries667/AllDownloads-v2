<script setup lang="ts">
import { ref, onMounted, watch } from "vue";
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
const settings = ref<any>({}),
  form = ref<any>({}),
  accounts = ref<any[]>([]),
  health = ref<any>({}),
  validation = ref<any>(null),
  saving = ref(false),
  scenario = ref("normal"),
  switching = ref(false),
  auth = ref<any>(null),
  authOpen = ref(false);
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
    const [s, a, h] = await Promise.all([
      api.get("/settings"),
      api.get("/accounts"),
      api.get("/health"),
    ]);
    settings.value = s;
    if (!Object.keys(form.value).length) form.value = { ...s };
    accounts.value = a.items;
    health.value = h;
    if (isDemo) scenario.value = (await api.get("/__demo/status")).scenario;
  } catch (e) {
    notifyError(e);
  }
}
async function validate() {
  try {
    validation.value = await api.post("/settings/validate", form.value);
  } catch (e) {
    notifyError(e);
  }
}
async function save() {
  saving.value = true;
  try {
    const result = await api.post("/settings/validate", form.value);
    validation.value = result;
    if (!result.valid) return;
    settings.value = await api.patch(
      "/settings",
      form.value,
      settings.value.revision,
    );
    form.value = { ...settings.value };
    notifySuccess("统一设置已保存。已有文件不会自动搬迁。");
  } catch (e) {
    notifyError(e);
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
  try {
    await api.post("/accounts/" + id + "/check");
    notifySuccess("已读取账号示例状态；未连接真实平台。");
  } catch (e) {
    notifyError(e);
  }
}
async function login(platform: string) {
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
          <el-form label-position="top"
            ><el-form-item label="统一本机下载根目录"
              ><el-input
                v-model="form.downloadRoot"
                aria-label="统一本机下载根目录"
                placeholder="D:\AllDownloadsData"
              />
              <p class="field-help">
                正式运行时位于 Y7000P
                的本机磁盘；本轮只保存演示配置，不创建目录。
              </p></el-form-item
            ><el-form-item label="Google Drive 归档目标"
              ><el-input
                v-model="form.archiveDestinationLabel"
                aria-label="Google Drive 归档目标"
              />
              <p class="field-help">
                云端目标与本机下载根独立配置。
              </p></el-form-item
            >
            <div class="form-two">
              <el-form-item label="同时下载数量"
                ><el-input-number
                  v-model="form.downloadConcurrency"
                  :min="1"
                  :max="10"
                  aria-label="同时下载数量" /></el-form-item
              ><el-form-item label="同时上传数量"
                ><el-input-number
                  v-model="form.uploadConcurrency"
                  :min="1"
                  :max="10"
                  aria-label="同时上传数量"
              /></el-form-item>
            </div>
            <p class="muted">
              显示时区：Asia/Shanghai。修改下载根前需处理在途任务；旧根文件保留原位。
            </p>
            <div v-if="validation" class="mb16">
              <el-alert
                :title="
                  validation.valid
                    ? '格式与模拟任务校验通过'
                    : '设置暂时无法应用'
                "
                :type="validation.valid ? 'success' : 'error'"
                :closable="false"
                show-icon
              />
              <p
                v-for="issue in validation.issues"
                :key="issue"
                class="text-danger"
              >
                {{ issue }}
              </p>
              <p class="muted">
                {{ validation.note }} · 受影响任务
                {{ validation.affectedTasks }}
              </p>
            </div>
            <div class="toolbar">
              <el-button :icon="ShieldCheck" @click="validate"
                >校验设置</el-button
              ><el-button
                type="primary"
                :icon="Save"
                :loading="saving"
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
              health.java === "UNAVAILABLE" ? "连接中断" : "演示替身"
            }}</strong>
          </div>
          <div class="health-row">
            <span>Go 执行程序</span
            ><strong>{{
              health.go === "UNAVAILABLE" ? "不可用" : "未连接"
            }}</strong>
          </div>
          <div class="health-row">
            <span>存储检查</span
            ><strong>{{
              health.storage === "INSUFFICIENT_SPACE" ? "空间不足" : "示例状态"
            }}</strong>
          </div>
          <p class="muted">
            关闭网页后，真实任务由后端持续运行。当前演示不会执行后台下载或上传。
          </p>
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
  </div>
</template>
