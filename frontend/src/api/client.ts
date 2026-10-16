import { ref } from "vue";
import { notifyError as attentionError, notifySuccess as attentionSuccess } from "../notifications/notify";
import type { Transport } from "./types";
export const dataRevision = ref(0);
export const isDemo = import.meta.env.VITE_API_MODE === "demo";
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
export function createHttpTransport(base = "/api/v1", timeoutMs = 20000): Transport {
  const prefix = base.replace(/\/+$/, "");
  return {
    async request<T>(
      method: string,
      path: string,
      body?: unknown,
      headers: Record<string, string> = {},
    ) {
      const controller = new AbortController();
      const timeout = setTimeout(() => controller.abort(), timeoutMs);
      let response: Response;
      let result;
      try {
        response = await fetch(prefix + path, {
          method,
          headers: { Accept: "application/json", ...(body === undefined ? {} : { "Content-Type": "application/json" }), ...headers },
          body: body === undefined ? undefined : JSON.stringify(body),
          signal: controller.signal,
        });
        if (response.status === 204 && response.ok) return undefined as T;
        try { result = await response.json(); }
        catch {
          if (controller.signal.aborted) throw new ApiError("REQUEST_TIMEOUT", "Java 接口响应超时，请确认服务状态后重试", null, true);
          throw new ApiError("INVALID_RESPONSE", "Java 接口未返回 JSON，请检查 /api/v1 代理与服务地址");
        }
      } catch (error) {
        if (error instanceof ApiError) throw error;
        throw new ApiError(controller.signal.aborted ? "REQUEST_TIMEOUT" : "NETWORK_ERROR",
          controller.signal.aborted ? "Java 接口响应超时，请确认服务状态后重试" : "无法连接 Java 业务接口，请确认服务已启动及接口代理配置", null, true);
      } finally { clearTimeout(timeout); }
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
// The production bundle has no demo transport or seed data. Demo is an explicit build mode.
const selectedTransport = isDemo
  ? import("./demo").then(module => module.createDemoTransport())
  : Promise.resolve(createHttpTransport(import.meta.env.VITE_API_BASE || "/api/v1"));
const transport: Transport = {
  async request(method, path, body, headers) { return (await selectedTransport).request(method, path, body, headers); },
};
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
