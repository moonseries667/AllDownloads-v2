export const platformName: Record<string, string> = {
  BILIBILI: "Bilibili",
  TELEGRAM: "Telegram",
  QUARK: "夸克网盘",
  GOOGLE_DRIVE: "Google Drive",
};
export function taskPhaseName(phase: string) {
  return ({ SYNC_BIL_SOURCE: "刷新 Bilibili 来源", FETCH_BIL_METADATA: "读取 Bilibili 信息",
    DOWNLOAD_MEDIA: "下载媒体", UPLOAD_ARTIFACT: "上传归档", VERIFY_ARCHIVE: "核对云端归档",
    VALIDATE_TARGET: "校验本机目录", SELECT_LOCAL_DIRECTORY: "选择本机目录", LIST_DRIVE_FOLDERS: "读取 Drive 目录",
    VALIDATE_DRIVE_TARGET: "校验 Drive 目标", CHECK_ACCOUNT: "检查平台账号", AUTH_STEP: "账号登录",
    LIST_QUARK_TREE: "刷新夸克目录", TRANSFER_QUARK_ITEMS: "夸克转存与追更", RECONCILE_QUARK_TRANSFER: "核对夸克转存结果",
  } as Record<string, string>)[phase] || phase;
}
export function formatDate(value: string | null | undefined) {
  if (!value || !Number.isFinite(new Date(value).getTime())) return "暂无记录";
  const p = new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Shanghai",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hourCycle: "h23",
  }).formatToParts(new Date(value));
  const v = (key: string) => p.find((i) => i.type === key)?.value;
  return (
    v("year") +
    "." +
    v("month") +
    "." +
    v("day") +
    "-" +
    v("hour") +
    "." +
    v("minute") +
    "." +
    v("second")
  );
}
export function formatBytes(value: string | number | null | undefined) {
  if (value == null || value === "") return "大小未知";
  const n = Number(value);
  if (!Number.isFinite(n) || n < 0) return "大小未知";
  if (n === 0) return "0 B";
  const i = Math.max(0, Math.min(Math.floor(Math.log(n) / Math.log(1024)), 4));
  return (
    (n / 1024 ** i).toFixed(i > 0 ? 1 : 0) +
    " " +
    ["B", "KB", "MB", "GB", "TB"][i]
  );
}
export function duration(value: number | null | undefined) {
  if (value == null || !Number.isFinite(value) || value < 0) return "时长未知";
  const seconds = Math.floor(value);
  const minutes = Math.floor(seconds / 60);
  return (minutes >= 60 ? Math.floor(minutes / 60) + ":" + String(minutes % 60).padStart(2, "0") : String(minutes)) +
    ":" + String(seconds % 60).padStart(2, "0");
}

export function calendarDate(value: string | null | undefined) {
  if (!value || !Number.isFinite(new Date(value).getTime())) return "日期未知";
  return new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Shanghai", year: "numeric", month: "long", day: "numeric",
  }).format(new Date(value));
}
