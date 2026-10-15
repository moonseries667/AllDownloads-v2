import { describe, expect, it } from "vitest";
import { calendarDate, duration, formatBytes } from "../src/format";

describe("media facts", () => {
  it("keeps unknown durations and sizes distinct from measured zero", () => {
    expect(formatBytes(null)).toBe("大小未知");
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes("bad")).toBe("大小未知");
    expect(duration(null)).toBe("时长未知");
    expect(duration(0)).toBe("0:00");
    expect(duration(3670.2)).toBe("1:01:10");
  });
  it("formats the upload calendar date in Shanghai without using refresh dates", () => {
    expect(calendarDate("2026-10-14T16:01:00Z")).toBe("2026年10月15日");
    expect(calendarDate(null)).toBe("日期未知");
  });
});
