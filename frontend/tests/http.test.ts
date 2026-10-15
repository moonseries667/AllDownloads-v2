import { afterEach, describe, expect, it, vi } from "vitest";
import { createApi, createHttpTransport } from "../src/api/client";
afterEach(() => vi.unstubAllGlobals());
describe("Java HTTP transport", () => {
  it("sends quoted revision preconditions and an idempotency key for controlled targets", async () => {
    const fetchMock = vi.fn().mockImplementation(async () => new Response(JSON.stringify({ data: { id: "s1" }, requestId: "request-1" })));
    vi.stubGlobal("fetch", fetchMock);
    const client = createApi(createHttpTransport(), () => {});
    await client.patch("/bilibili/sources/s1", { pinned: true }, "revision-1");
    expect(fetchMock.mock.calls[0][1].headers["If-Match"]).toBe('"revision-1"');
    await client.post("/download-targets/validate", { path: "D:\\本批目录" });
    expect(fetchMock.mock.calls[1][1].headers["Idempotency-Key"]).toMatch(/^[a-f0-9-]{36}$/);
    expect(JSON.parse(fetchMock.mock.calls[1][1].body)).toEqual({ path: "D:\\本批目录" });
  });
  it("preserves exact track selections and target references across the HTTP boundary", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { previewId: "pv-1", taskId: null }, requestId: "r" }), { status: 202 }));
    vi.stubGlobal("fetch", fetchMock);
    const body = { platform: "BILIBILI", selection: { mode: "EXPLICIT", resource: "BILIBILI_CONTENTS", ids: ["c1"],
      mediaSelections: [{ contentId: "c1", contentRevision: "rev1", trackIds: ["audio", "sub-en"] }] }, target: { id: "target-1", revision: "2" } };
    await createApi(createHttpTransport(), () => {}).post("/download-previews", body);
    expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toEqual(body);
  });
  it("preserves actionable errors without treating malformed responses as success", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ error: { code: "INVALID_STORAGE_ROOT", message: "目录不可写", retryable: false, action: "CHANGE_TARGET", details: {} }, requestId: "req-target" }), { status: 422 }))
      .mockResolvedValueOnce(new Response("null")));
    const client = createHttpTransport();
    await expect(client.request("POST", "/download-targets/validate", { path: "D:\\test" })).rejects.toMatchObject({
      code: "INVALID_STORAGE_ROOT", action: "CHANGE_TARGET", requestId: "req-target", retryable: false,
    });
    await expect(client.request("GET", "/overview")).rejects.toMatchObject({ code: "INVALID_RESPONSE" });
  });
  it("unwraps Java data while preserving opaque identifiers and explicit request headers", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(
        new Response(
          JSON.stringify({
            data: { orderId: "90071992547409930" },
            requestId: "req-1",
          }),
          { status: 202 },
        ),
      );
    vi.stubGlobal("fetch", fetchMock);
    const data = await createHttpTransport().request(
      "POST",
      "/download-orders",
      { previewId: "pv-1" },
      { "Idempotency-Key": "request-1" },
    );
    expect(data).toEqual({ orderId: "90071992547409930" });
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/download-orders",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({ "Idempotency-Key": "request-1" }),
        body: JSON.stringify({ previewId: "pv-1" }),
      }),
    );
  });
  it("retains Java conflict code and explanation for re-preview", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(
          new Response(
            JSON.stringify({
              error: {
                code: "VERSION_CHANGED",
                message: "文件版本变化",
                details: { nodeId: "n1" },
              },
              requestId: "req-2",
            }),
            { status: 409 },
          ),
        ),
    );
    await expect(
      createHttpTransport().request("POST", "/download-orders", {
        previewId: "old",
      }),
    ).rejects.toMatchObject({
      code: "VERSION_CHANGED",
      message: "文件版本变化",
      details: { nodeId: "n1" },
    });
  });
  it("does not convert rejected network requests into successful data", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockRejectedValue(new TypeError("Failed to fetch")),
    );
    await expect(
      createHttpTransport().request("GET", "/tasks"),
    ).rejects.toThrow("Failed to fetch");
  });
});
