export type Platform = "BILIBILI" | "TELEGRAM" | "QUARK";
export type RecordData = Record<string, any>;
export interface Page<T> {
  items: T[];
  total: number | null;
  nextCursor: string | null;
}
export interface Selection {
  mode: "EXPLICIT" | "QUERY";
  resource:
    | "BILIBILI_CONTENTS"
    | "BILIBILI_PARTS"
    | "QUARK_NODES"
    | "TELEGRAM_ATTACHMENTS";
  ids?: string[];
  filters?: Record<string, string>;
  excludeIds?: string[];
}
export interface Preview {
  id: string;
  status: string;
  platform: Platform;
  expiresAt: string;
  storageRevision: string;
  items: RecordData[];
  summary: { download: number; reuse: number; blocked: number; total: number };
  warnings: string[];
}
export interface Transport {
  request<T = any>(
    method: string,
    path: string,
    body?: unknown,
    headers?: Record<string, string>,
  ): Promise<T>;
}
