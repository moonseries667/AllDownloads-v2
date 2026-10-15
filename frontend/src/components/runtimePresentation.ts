export interface RuntimeFacts {
  state: string;
  observedAt?: string | null;
  services: { id: string; required: boolean; state: string; reasonCode?: string | null }[];
  mount?: { state: string; reasonCode?: string | null; observedAt?: string | null };
}
export function serviceSummary(runtime?: RuntimeFacts | null) {
  const required = runtime?.services?.filter(s => s.required) || [];
  if (!required.length) return { label: "服务状态未知", tone: "muted" };
  if (required.some(s => s.state === "ERROR")) return { label: "服务异常", tone: "bad" };
  if (required.some(s => s.state === "NOT_CONFIGURED")) return { label: "服务未配置", tone: "warn" };
  if (required.some(s => s.state === "LOST")) return { label: required.some(s => s.state === "ACTIVE") ? "部分服务启动" : "服务不可用", tone: "bad" };
  if (required.every(s => s.state === "ACTIVE") && runtime?.state === "RUNNING") return { label: "服务全部启动", tone: "good" };
  return { label: "服务状态未知", tone: "muted" };
}
export const serviceLabels: Record<string, string> = { ACTIVE: "运行中", LOST: "已失联", ERROR: "异常", NOT_CONFIGURED: "未配置", UNKNOWN: "未知" };
export const mountLabels: Record<string, string> = { MOUNTED: "网盘已挂载", NOT_MOUNTED: "网盘未挂载", NOT_CONFIGURED: "网盘未配置", UNKNOWN: "挂载状态未知", UNSUPPORTED: "无法探测挂载" };
