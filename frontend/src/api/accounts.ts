// Public consumer of contracts/ACCOUNTS.md; never accepts private login inputs.
import { api } from "./client";
export type AccountPlatform = "BILIBILI" | "TELEGRAM" | "QUARK" | "GOOGLE_DRIVE";
export const accountIds: Record<AccountPlatform, string> = {
  BILIBILI: "bilibili-main", TELEGRAM: "telegram-main", QUARK: "quark-main", GOOGLE_DRIVE: "drive-main",
};
export interface Account {
  id: string; platform: AccountPlatform; label: string;
  state: "ACTIVE" | "AUTH_REQUIRED" | "UNKNOWN" | "ERROR";
  lastCheckedAt: string | null; reasonCode: string | null; revision: string;
}
export interface AuthSession {
  authSessionId: string; accountId: string; platform: AccountPlatform;
  state: "PENDING" | "SUCCEEDED" | "CANCELLED" | "EXPIRED" | "NEEDS_ACTION";
  nextAction: "WAIT" | "SCAN_QR" | "OPEN_URL" | "LOCAL_SETUP" | "NONE";
  url: string | null; message: string | null; expiresAt: string;
  revision: string; taskId: string | null; reasonCode: string | null;
}
export function createAccountsApi(client: Pick<typeof api, "get" | "post" | "delete">) {
  const path = (id: string) => "/auth-sessions/" + encodeURIComponent(id);
  return {
    list: () => client.get<{ items: Account[]; nextCursor: null }>("/accounts"),
    check: (id: string) => client.post<{ accountId: string; taskId: string }>("/accounts/" + encodeURIComponent(id) + "/check"),
    begin: (platform: AccountPlatform) => client.post<AuthSession>("/auth-sessions", { platform }),
    session: (id: string) => client.get<AuthSession>(path(id)),
    poll: (id: string) => client.post<AuthSession>(path(id) + "/steps", { action: "POLL" }),
    cancel: (session: AuthSession) => client.delete<AuthSession>(path(session.authSessionId), session.revision),
    task: (id: string) => client.get<{ status: string }>("/tasks/" + encodeURIComponent(id)),
  };
}
export const accountsApi = createAccountsApi(api);

export function officialAuthUrl(session: Pick<AuthSession, "platform" | "url">): string | null {
  if (!session.url) return null;
  try {
    const url = new URL(session.url);
    if (url.protocol !== "https:" || url.username || url.password) return null;
    const domains: Record<AccountPlatform, string[]> = {
      BILIBILI: ["bilibili.com"], TELEGRAM: ["telegram.org", "t.me"],
      QUARK: ["quark.cn"], GOOGLE_DRIVE: ["accounts.google.com"],
    };
    return domains[session.platform].some(domain => url.hostname === domain || url.hostname.endsWith("." + domain)) ? url.href : null;
  } catch { return null; }
}
