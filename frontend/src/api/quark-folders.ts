import type { Page, Transport } from "./types";

// contracts/FEEDBACK-PART1.md at 6233234: paths are display-only;
// the target identity is always the server's id and revision.
export interface QuarkFolder {
  id: string;
  accountId: string;
  parentId: string | null;
  name: string;
  kind: "FOLDER";
  revision: string;
  selectable: boolean;
  availability: string;
  availabilityReason: string | null;
  path: string;
}
export interface QuarkFolderPage extends Page<QuarkFolder> {
  parent: QuarkFolder | null;
  breadcrumbs: QuarkFolder[];
  status: "READY" | "LOADING" | "NEEDS_ACTION";
  taskId: string | null;
}
export interface QuarkTarget {
  id: string;
  revision: string;
  path: string;
}
export function folderPath(page: QuarkFolderPage): string {
  const chain = [...page.breadcrumbs];
  if (page.parent && !chain.some((node) => node.id === page.parent!.id))
    chain.push(page.parent);
  return chain.filter((node) => node.parentId !== null).map((node) => node.name).join("/");
}
export function selectableFolder(page: QuarkFolderPage): QuarkTarget | null {
  const folder = page.parent;
  if (page.status !== "READY" || !folder?.selectable || folder.availability !== "AVAILABLE") return null;
  return { id: folder.id, revision: folder.revision, path: folderPath(page) };
}
export function loadQuarkFolders(transport: Pick<Transport, "request">, accountId: string, parentId?: string, cursor?: string) {
  const query = new URLSearchParams({ accountId });
  if (parentId !== undefined) query.set("parentId", parentId);
  if (cursor) query.set("cursor", cursor);
  return transport.request<QuarkFolderPage>("GET", "/quark/folders?" + query.toString());
}
