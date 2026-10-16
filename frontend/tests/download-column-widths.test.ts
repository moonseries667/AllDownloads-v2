import { describe, expect, it } from "vitest";
import { resizeColumn, toggleColumn, type ColumnWidth } from "../src/download-column-widths";
describe("download column widths", () => {
  const initial: ColumnWidth = { width: 280, min: 160, mode: "manual" };
  it("alternates fit and minimum even when content is narrower than the minimum", () => {
    const fit = toggleColumn(initial, 402.4);
    expect(fit.width).toBe(403);
    const min = toggleColumn(fit, 800);
    expect(min.width).toBe(160);
    expect(toggleColumn(min, 510).width).toBe(510);
    expect(toggleColumn(initial, 2).width).toBe(160);
  });
  it("manual resizing clamps and resets the next double click to fit", () => {
    const dragged = resizeColumn(toggleColumn(initial, 400), -20);
    expect(dragged).toEqual({ width: 160, min: 160, mode: "manual" });
    expect(toggleColumn(dragged, 600).width).toBe(600);
  });
});
