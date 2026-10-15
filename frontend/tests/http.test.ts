import { afterEach, describe, expect, it, vi } from "vitest";
import { createHttpTransport } from "../src/api/client";
afterEach(() => vi.unstubAllGlobals());
describe("Java HTTP transport", () => {
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
