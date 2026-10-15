import { ref } from "vue";
import { ElNotification } from "element-plus";
import type { Transport } from "./types";
import { createDemoTransport } from "./demo";
export const dataRevision = ref(0);
export const isDemo = import.meta.env.VITE_API_MODE !== "http";
export class ApiError extends Error {
  constructor(
    public code: string,
    message: string,
    public details: unknown = null,
  ) {
    super(message);
  }
}
export function createHttpTransport(base = "/api/v1"): Transport {
  return {
    async request<T>(
      method: string,
      path: string,
      body?: unknown,
      headers: Record<string, string> = {},
    ) {
      const response = await fetch(base + path, {
        method,
        headers: { "Content-Type": "application/json", ...headers },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      const result = await response.json();
      if (!response.ok || result.error)
        throw new ApiError(
          result.error?.code ?? "HTTP_ERROR",
          result.error?.message ?? "请求失败 (" + response.status + ")",
          result.error?.details,
        );
      return result.data as T;
    },
  };
}
const transport: Transport = isDemo
  ? createDemoTransport()
  : createHttpTransport(import.meta.env.VITE_API_BASE || "/api/v1");
const mutation = async <T = any>(
  method: string,
  path: string,
  body?: unknown,
  revision?: string,
) => {
  const headers: Record<string, string> = {};
  if (method === "POST") headers["Idempotency-Key"] = crypto.randomUUID();
  if (revision) headers["If-Match"] = revision;
  const result = await transport.request<T>(method, path, body, headers);
  dataRevision.value++;
  return result;
};
export const api = {
  get: <T = any>(path: string) => transport.request<T>("GET", path),
  post: <T = any>(path: string, body?: unknown, revision?: string) =>
    mutation<T>("POST", path, body, revision),
  patch: <T = any>(path: string, body?: unknown, revision?: string) =>
    mutation<T>("PATCH", path, body, revision),
  delete: <T = any>(path: string, revision?: string) =>
    mutation<T>("DELETE", path, undefined, revision),
};
const recentErrors = new Map<string, number>();
export function notifyError(error: unknown) {
  const message = error instanceof Error ? error.message : "请求失败，请重试";
  const now = Date.now();
  if (now - (recentErrors.get(message) ?? 0) < 60000) return;
  for (const [key, time] of recentErrors)
    if (now - time >= 60000) recentErrors.delete(key);
  recentErrors.set(message, now);
  ElNotification({
    title: "操作未完成",
    message,
    type: "error",
    position: "top-right",
  });
}
export function notifySuccess(message: string) {
  ElNotification({
    title: isDemo ? "演示操作已记录" : "已受理",
    message,
    type: "success",
    position: "top-right",
    duration: 3000,
  });
}
