import { describe, expect, it } from "vitest";
import { createDemoTransport, validateTelegramLinks } from "../src/api/demo";
import { createSeed } from "../src/api/fixtures";

function harness(scenario?: string) {
  const state = createSeed();
  if (scenario) state.scenario = scenario;
  const transport = createDemoTransport({ persist: false, state });
  const request = (
    method: string,
    path: string,
    body?: unknown,
    headers: Record<string, string> = {},
  ) => transport.request(method, path, body, headers);
  return { state, transport, request };
}

async function expectCode(promise: Promise<unknown>, code: string) {
  await expect(promise).rejects.toMatchObject({ code });
}

function newTasksSince(
  harnessValue: ReturnType<typeof harness>,
  beforeIds: Set<string>,
) {
  return harnessValue.transport
    .inspect()
    .tasks.filter((task) => !beforeIds.has(task.id));
}

describe("demo transport business boundaries", () => {
  it("keeps initial GET requests read-only with respect to tasks and orders", async () => {
    const h = harness();
    const before = h.transport.inspect();
    await Promise.all([
      h.request("GET", "/overview"),
      h.request("GET", "/bilibili/sources"),
      h.request("GET", "/bilibili/contents"),
      h.request("GET", "/telegram/messages"),
      h.request("GET", "/telegram/resources"),
      h.request("GET", "/telegram/watcher?accountId=tg-main"),
      h.request("GET", "/quark/shares"),
      h.request("GET", "/quark/nodes"),
      h.request("GET", "/download-orders"),
      h.request("GET", "/artifacts"),
      h.request("GET", "/tasks"),
    ]);
    const after = h.transport.inspect();
    expect(after.tasks).toHaveLength(before.tasks.length);
    expect(after.orders).toHaveLength(before.orders.length);
  });

  it("adds and refreshes Bilibili sources as INFO_SYNC without creating orders", async () => {
    const h = harness();
    const before = h.transport.inspect();
    const result = (await h.request("POST", "/bilibili/sources", {
      links: ["https://space.bilibili.com/4242/favlist?fid=98765"],
    })) as any;
    const sourceId = result.items[0].sourceId;
    await h.request("POST", "/bilibili/sources/" + sourceId + "/refresh", {
      force: false,
    });

    const added = newTasksSince(
      h,
      new Set(before.tasks.map((task) => task.id)),
    );
    expect(added).toHaveLength(2);
    expect(added.map((task) => task.kind)).toEqual(["INFO_SYNC", "INFO_SYNC"]);
    expect(added.every((task) => task.platform === "BILIBILI")).toBe(true);
    expect(h.transport.inspect().orders).toHaveLength(before.orders.length);
  });

  it("adds and refreshes Quark shares without creating download tasks or orders", async () => {
    const h = harness();
    const before = h.transport.inspect();
    const result = (await h.request("POST", "/quark/shares", {
      accountId: "q-main",
      entries: [
        {
          url: "https://pan.quark.cn/s/NewShare20261015",
          targetFolderId: "folder-inbox",
        },
      ],
    })) as any;
    const shareId = result.items[0].shareId;
    await h.request("POST", "/quark/shares/" + shareId + "/refresh");

    const added = newTasksSince(
      h,
      new Set(before.tasks.map((task) => task.id)),
    );
    expect(added.map((task) => task.kind)).toEqual([
      "TRANSFER_SYNC",
      "TRANSFER_SYNC",
    ]);
    expect(added.every((task) => task.platform === "QUARK")).toBe(true);
    expect(added.some((task) => task.kind === "DOWNLOAD")).toBe(false);
    expect(h.transport.inspect().orders).toHaveLength(before.orders.length);
  });

  it("expands nested Quark folders into a fixed preview item list", async () => {
    const h = harness();
    const result = (await h.request("POST", "/download-previews", {
      platform: "QUARK",
      selection: {
        mode: "EXPLICIT",
        resource: "QUARK_NODES",
        ids: ["q1-folder"],
      },
    })) as any;
    const beforePreview = (await h.request(
      "GET",
      "/download-previews/" + result.previewId,
    )) as any;
    expect(beforePreview.items.map((item: any) => item.nodeId).sort()).toEqual([
      "q1-sub1",
      "q1-sub2",
    ]);

    const sourceNode = h.state.nodes.find(
      (node) => node.id === "q1-sub1",
    ) as any;
    h.state.nodes.push({
      ...sourceNode,
      id: "q1-sub3",
      externalId: "q1-sub3",
      name: "03 新增练习.mp4",
      relativePath: "摄影课程/03 新增练习.mp4",
      version: "v1",
    });
    const afterNodeAddition = (await h.request(
      "GET",
      "/download-previews/" + result.previewId,
    )) as any;
    expect(afterNodeAddition.items).toEqual(beforePreview.items);
    expect(
      afterNodeAddition.items.some((item: any) => item.nodeId === "q1-sub3"),
    ).toBe(false);
  });

  it("creates no download order for previewing and creates one only after order submission", async () => {
    const h = harness();
    const before = h.transport.inspect();
    const preview = (await h.request("POST", "/download-previews", {
      platform: "BILIBILI",
      selection: {
        mode: "EXPLICIT",
        resource: "BILIBILI_CONTENTS",
        ids: ["b1"],
      },
    })) as any;
    expect(h.transport.inspect().orders).toHaveLength(before.orders.length);
    expect(h.transport.inspect().tasks).toHaveLength(before.tasks.length);

    const submitted = (await h.request("POST", "/download-orders", {
      previewId: preview.previewId,
    })) as any;
    const after = h.transport.inspect();
    expect(submitted.reused).toBe(false);
    expect(after.orders).toHaveLength(before.orders.length + 1);
    expect(after.tasks.filter((task) => task.kind === "DOWNLOAD")).toHaveLength(
      before.tasks.filter((task) => task.kind === "DOWNLOAD").length + 1,
    );
  });

  it("reuses an order when the same preview is submitted again", async () => {
    const h = harness();
    const preview = (await h.request("POST", "/download-previews", {
      platform: "QUARK",
      selection: {
        mode: "EXPLICIT",
        resource: "QUARK_NODES",
        ids: ["q1-sub1"],
      },
    })) as any;
    const beforeOrders = h.transport.inspect().orders.length;
    const first = (await h.request("POST", "/download-orders", {
      previewId: preview.previewId,
    })) as any;
    const second = (await h.request("POST", "/download-orders", {
      previewId: preview.previewId,
    })) as any;

    expect(second.orderId).toBe(first.orderId);
    expect(second.reused).toBe(true);
    expect(h.transport.inspect().orders).toHaveLength(beforeOrders + 1);
  });

  it("rejects a preview when a selected Quark file version changes", async () => {
    const h = harness();
    const preview = (await h.request("POST", "/download-previews", {
      platform: "QUARK",
      selection: {
        mode: "EXPLICIT",
        resource: "QUARK_NODES",
        ids: ["q1-sub1"],
      },
    })) as any;
    const file = h.state.nodes.find((node) => node.id === "q1-sub1") as any;
    file.version = "v2";

    await expectCode(
      h.request("POST", "/download-orders", { previewId: preview.previewId }),
      "VERSION_CHANGED",
    );
    expect(h.transport.inspect().orders).toHaveLength(1);
  });

  it("rejects order submission when the demo scenario has insufficient space", async () => {
    const h = harness("disk");
    const preview = (await h.request("POST", "/download-previews", {
      platform: "QUARK",
      selection: {
        mode: "EXPLICIT",
        resource: "QUARK_NODES",
        ids: ["q1-sub1"],
      },
    })) as any;

    await expectCode(
      h.request("POST", "/download-orders", { previewId: preview.previewId }),
      "INSUFFICIENT_SPACE",
    );
    expect(h.transport.inspect().orders).toHaveLength(1);
  });

  it("rejects enabling the Telegram watcher before initial scan completion", async () => {
    const h = harness();
    await expectCode(
      h.request("PATCH", "/telegram/watcher", {
        accountId: "tg-main",
        enabled: true,
      }),
      "INITIAL_SCAN_REQUIRED",
    );
    expect(h.transport.inspect().watcher.enabled).toBe(false);
  });

  it("accepts public, private, topic, comment and thread links, then deduplicates exact repeats", async () => {
    const links = [
      "https://t.me/channelTest/123",
      "https://t.me/c/123456789/321",
      "https://t.me/channelTest/12/456",
      "https://t.me/channelTest/789?comment=345&thread=678",
      "https://t.me/channelTest/123",
    ];
    expect(validateTelegramLinks(links)).toEqual(links.slice(0, 4));

    const h = harness();
    const before = h.transport.inspect();
    const result = (await h.request("POST", "/telegram/link-downloads", {
      accountId: "tg-main",
      links,
    })) as any;
    expect(result.count).toBe(4);
    const added = newTasksSince(
      h,
      new Set(before.tasks.map((task) => task.id)),
    );
    expect(added).toHaveLength(1);
    expect(added[0].platform).toBe("TELEGRAM");
    expect(added[0].kind).toBe("DOWNLOAD");
  });

  it("accepts exactly 200 distinct Telegram message links", async () => {
    const links = Array.from(
      { length: 200 },
      (_, index) => "https://t.me/channelTest/" + (index + 1),
    );
    const h = harness();
    const result = (await h.request("POST", "/telegram/link-downloads", {
      accountId: "tg-main",
      links,
    })) as any;
    expect(result.count).toBe(200);
  });

  it("rejects a Telegram batch larger than 200 without creating a task", async () => {
    const links = Array.from(
      { length: 201 },
      (_, index) => "https://t.me/channelTest/" + (index + 1),
    );
    const h = harness();
    const before = h.transport.inspect().tasks.length;

    await expectCode(
      h.request("POST", "/telegram/link-downloads", {
        accountId: "tg-main",
        links,
      }),
      "INVALID_INPUT",
    );
    expect(h.transport.inspect().tasks).toHaveLength(before);
  });

  it("does not allow PAUSE for Telegram tasks", async () => {
    const h = harness();
    const created = (await h.request("POST", "/telegram/saved-sync", {
      accountId: "tg-main",
    })) as any;
    const task = h.transport
      .inspect()
      .tasks.find((item) => item.id === created.taskId) as any;
    expect(task.allowedActions).toEqual(["CANCEL"]);

    await expectCode(
      h.request("POST", "/tasks/" + created.taskId + "/actions", {
        action: "PAUSE",
      }),
      "ACTION_NOT_ALLOWED",
    );
  });

  it("retries an upload without creating a download task or order", async () => {
    const h = harness();
    const before = h.transport.inspect();
    const result = (await h.request("POST", "/artifacts/a1/archive")) as any;
    const after = h.transport.inspect();
    const upload = after.tasks.find((task) => task.id === result.taskId);

    expect(upload?.kind).toBe("UPLOAD");
    expect(after.tasks.filter((task) => task.kind === "DOWNLOAD")).toHaveLength(
      before.tasks.filter((task) => task.kind === "DOWNLOAD").length,
    );
    expect(after.orders).toHaveLength(before.orders.length);
  });

  it("limits cleanup preview and submission to locally verified, cloud verified artifacts", async () => {
    const h = harness();
    const preview = (await h.request("POST", "/cleanup-previews", {})) as any;
    expect(preview.items.map((item: any) => item.id)).toEqual(["a4"]);

    await h.request("POST", "/cleanup-orders", { previewId: preview.id });
    const after = h.transport.inspect();
    expect(after.artifacts.find((item) => item.id === "a4")?.localState).toBe(
      "CLEANED",
    );
    expect(after.artifacts.find((item) => item.id === "a1")?.localState).toBe(
      "VERIFIED",
    );
    expect(after.artifacts.find((item) => item.id === "a2")?.localState).toBe(
      "PARTIAL",
    );
    expect(after.artifacts.find((item) => item.id === "a3")?.localState).toBe(
      "VERIFIED",
    );
  });

  it("replays an idempotent request with the same body and conflicts on a changed body", async () => {
    const h = harness();
    const headers = { "Idempotency-Key": "demo-test-key" };
    const body = { accountId: "tg-main" };
    const first = (await h.request(
      "POST",
      "/telegram/saved-sync",
      body,
      headers,
    )) as any;
    const tasksAfterFirst = h.transport.inspect().tasks.length;
    const replay = (await h.request(
      "POST",
      "/telegram/saved-sync",
      body,
      headers,
    )) as any;

    expect(replay).toEqual(first);
    expect(h.transport.inspect().tasks).toHaveLength(tasksAfterFirst);
    await expectCode(
      h.request(
        "POST",
        "/telegram/saved-sync",
        { accountId: "tg-other" },
        headers,
      ),
      "IDEMPOTENCY_CONFLICT",
    );
  });
});
