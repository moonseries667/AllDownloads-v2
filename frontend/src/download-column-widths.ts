export interface ColumnWidth { width: number; min: number; mode: "manual" | "fit" | "min" }
export function resizeColumn(column: ColumnWidth, width: number): ColumnWidth {
  return { ...column, width: Math.max(column.min, Math.round(width)), mode: "manual" };
}
/** A drag resets the next double click to fit; repeated double clicks alternate. */
export function toggleColumn(column: ColumnWidth, contentWidth: number): ColumnWidth {
  return column.mode === "fit"
    ? { ...column, width: column.min, mode: "min" }
    : { ...column, width: Math.max(column.min, Math.ceil(contentWidth)), mode: "fit" };
}
