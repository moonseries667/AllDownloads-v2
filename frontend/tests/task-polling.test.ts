import { describe, expect, it } from "vitest";
import { taskPollingDelay } from "../src/task-polling";

describe("polling from task facts instead of removed overview counters", () => {
  it("keeps active and cancellation-pending tasks at the active cadence", () => {
    for (const status of ["QUEUED", "RUNNING", "PAUSING"]) {
      expect(
        taskPollingDelay({ items: [{ status }], nextCursor: null }, false),
      ).toBe(2000);
    }
    expect(
      taskPollingDelay(
        {
          items: [{ status: "NEEDS_ACTION", desiredAction: "CANCEL" }],
          nextCursor: null,
        },
        false,
      ),
    ).toBe(2000);
  });
  it("slows down when the complete task page contains only idle or terminal tasks", () => {
    expect(taskPollingDelay({ items: [], nextCursor: null }, false)).toBe(
      10000,
    );
    expect(
      taskPollingDelay(
        {
          items: [
            { status: "PAUSED" },
            { status: "SUCCEEDED", desiredAction: "CANCEL" },
          ],
          nextCursor: null,
        },
        false,
      ),
    ).toBe(10000);
  });
  it("does not treat unknown or partly loaded tasks as idle", () => {
    expect(taskPollingDelay(null, false)).toBe(2000);
    expect(
      taskPollingDelay(
        { items: [{ status: "SUCCEEDED" }], nextCursor: "next" },
        false,
      ),
    ).toBe(2000);
  });
  it("reduces work for a hidden tab regardless of active tasks", () => {
    expect(
      taskPollingDelay(
        { items: [{ status: "RUNNING" }], nextCursor: null },
        true,
      ),
    ).toBe(15000);
  });
});
