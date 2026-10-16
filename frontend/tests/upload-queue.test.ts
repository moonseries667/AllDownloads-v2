import { describe, expect, it } from "vitest";
import { uploadGroup, uploadPercent, uploadStateLabel } from "../src/components/upload-queue";
describe("upload presentation facts", () => {
  it("does not treat transmission 100 percent as remote verification", () => {
    expect(uploadPercent({ doneBytes: "100", totalBytes: "100", doneItems: 1, totalItems: 1 })).toBe(100);
    expect(uploadGroup("VERIFYING")).toBe("active");
    expect(uploadStateLabel({ state: "VERIFYING", desiredAction: "CONTINUE" })).toBe("核验中");
    expect(uploadGroup("VERIFIED")).toBe("completed");
    expect(uploadGroup("SUCCEEDED")).toBe("attention");
  });
  it("keeps unknown totals indeterminate and pause intent pending", () => {
    expect(uploadPercent({ doneBytes: "0", totalBytes: null, doneItems: 0, totalItems: null })).toBeNull();
    expect(uploadStateLabel({ state: "UPLOADING", desiredAction: "PAUSE" })).toBe("正在暂停");
    expect(uploadStateLabel({ state: "VERIFIED", desiredAction: "PAUSE" })).toBe("上传完成");
    expect(uploadStateLabel({ state: "PAUSED", desiredAction: "PAUSE" })).toBe("已暂停");
  });
});
