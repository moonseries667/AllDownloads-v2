export const platformName: Record<string, string> = {
  BILIBILI: "Bilibili",
  TELEGRAM: "Telegram",
  QUARK: "夸克网盘",
  GOOGLE_DRIVE: "Google Drive",
};
export function formatDate(value: string | null | undefined) {
  if (!value) return "暂无记录";
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
  if (value == null) return "大小未知";
  const n = Number(value);
  if (n === 0) return "0 B";
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), 4);
  return (
    (n / 1024 ** i).toFixed(i > 0 ? 1 : 0) +
    " " +
    ["B", "KB", "MB", "GB", "TB"][i]
  );
}
export function duration(value: number) {
  return Math.floor(value / 60) + ":" + String(value % 60).padStart(2, "0");
}
