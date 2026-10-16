import { ref } from "vue";
import { notifyError as attentionError, notifySuccess as attentionSuccess } from "../notifications/notify";
import type { Transport } from "./types";
import { createDemoTransport } from "./demo";
export const dataRevision = ref(0);
export const isDemo = import.meta.env.VITE_API_MODE !== "http";
export class ApiError extends Error {
  constructor(
    public code: string,
    message: string,
    public details: unknown = null,
    public retryable = false,
    public action: string | null = null,
    public requestId: string | null = null,
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
      if (response.status === 204 && response.ok) return undefined as T;
      let result;
      try { result = await response.json(); }
      catch { throw new ApiError("INVALID_RESPONSE", "服务返回了无法读取的响应，请稍后刷新"); }
      if (!result || typeof result !== "object") throw new ApiError("INVALID_RESPONSE", "服务返回了无法读取的响应，请稍后刷新");
      if (!response.ok || result.error)
        throw new ApiError(
          result.error?.code ?? "HTTP_ERROR",
          result.error?.message ?? "请求失败 (" + response.status + ")",
          result.error?.details,
          result.error?.retryable ?? false,
          result.error?.action ?? null,
          result.requestId ?? null,
        );
      if (!("data" in result)) throw new ApiError("INVALID_RESPONSE", "服务响应缺少数据，请稍后刷新");
      return result.data as T;
    },
  };
}
const transport: Transport = isDemo
  ? createDemoTransport()
  : createHttpTransport(import.meta.env.VITE_API_BASE || "/api/v1");
export function createApi(transport: Transport, onMutation = () => { dataRevision.value++; }, onError: (error: unknown) => void = () => {}) {
const request: Transport["request"] = async (method, path, body, headers) => {
  try { return await transport.request(method, path, body, headers); }
  catch (error) { onError(error); throw error; }
};
const mutation = async <T = any>(
  method: string,
  path: string,
  body?: unknown,
  revision?: string,
) => {
  const headers: Record<string, string> = {};
  if (method === "POST") headers["Idempotency-Key"] = crypto.randomUUID();
  if (revision) headers["If-Match"] = revision.startsWith('"') ? revision : '"' + revision + '"';
  const result = await request<T>(method, path, body, headers);
  onMutation();
  return result;
};
return {
  get: <T = any>(path: string) => request<T>("GET", path),
  post: <T = any>(path: string, body?: unknown, revision?: string) =>
    mutation<T>("POST", path, body, revision),
  patch: <T = any>(path: string, body?: unknown, revision?: string) =>
    mutation<T>("PATCH", path, body, revision),
  delete: <T = any>(path: string, revision?: string) =>
    mutation<T>("DELETE", path, undefined, revision),
};
}
export const api = createApi(transport, undefined, attentionError);
export const notifyError = attentionError;
export function notifySuccess(message: string) {
  return attentionSuccess(message, isDemo ? "演示操作已记录" : "已受理");
}
