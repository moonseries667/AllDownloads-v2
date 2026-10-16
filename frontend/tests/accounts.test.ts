import { describe, expect, it, vi } from "vitest";
import { createAccountsApi, officialAuthUrl, type AuthSession } from "../src/api/accounts";

describe("frozen public account API", () => {
  it("sends only platform, POLL and the exact revision for cancellation", async () => {
    const client = { get: vi.fn(), post: vi.fn(), delete: vi.fn() };
    const api = createAccountsApi(client);
    await api.begin("BILIBILI");
    await api.poll("session/opaque");
    await api.cancel({ authSessionId: "session/opaque", revision: "rev-2" } as AuthSession);
    expect(client.post.mock.calls).toEqual([
      ["/auth-sessions", { platform: "BILIBILI" }],
      ["/auth-sessions/session%2Fopaque/steps", { action: "POLL" }],
    ]);
    expect(client.delete).toHaveBeenCalledWith("/auth-sessions/session%2Fopaque", "rev-2");
  });
  it("reads status without causing platform checks or login steps", async () => {
    const client = { get: vi.fn(), post: vi.fn(), delete: vi.fn() };
    const api = createAccountsApi(client);
    await api.list(); await api.session("s1");
    expect(client.post).not.toHaveBeenCalled();
    expect(client.get.mock.calls).toEqual([["/accounts"], ["/auth-sessions/s1"]]);
    await api.check("telegram-main");
    expect(client.post).toHaveBeenCalledWith("/accounts/telegram-main/check");
  });
  it("allows official HTTPS authorization and QR URLs", () => {
    expect(officialAuthUrl({ platform: "BILIBILI", url: "https://passport.bilibili.com/h5-app/passport/login/scan?fixture=1" })).toContain("passport.bilibili.com");
    expect(officialAuthUrl({ platform: "GOOGLE_DRIVE", url: "https://accounts.google.com/o/oauth2/v2/auth?client_id=public" })).toContain("accounts.google.com");
  });
  it("never renders an unofficial or executable URL as an authorization action", () => {
    for (const url of ["javascript:alert(1)", "http://passport.bilibili.com/", "https://bilibili.com.attacker.invalid/", "https://user:password@passport.bilibili.com/"])
      expect(officialAuthUrl({ platform: "BILIBILI", url })).toBeNull();
    expect(officialAuthUrl({ platform: "GOOGLE_DRIVE", url: "https://passport.bilibili.com/" })).toBeNull();
  });
});
