import type { QuarkFolder, QuarkFolderPage } from "./quark-folders";

function folder(id: string, parentId: string | null, name: string, path: string, selectable = true): QuarkFolder {
  return { id, accountId: "q-main", parentId, name, path, kind: "FOLDER", revision: "1", selectable, availability: "AVAILABLE", availabilityReason: selectable ? null : "此文件夹没有写入权限" };
}
export const quarkFolderFixtures: QuarkFolder[] = [
  folder("0", null, "我的云盘", ""),
  folder("folder-learning", "0", "学习资料", "学习资料"),
  folder("folder-inbox", "0", "收件箱", "收件箱"),
  folder("folder-course", "folder-learning", "课程", "学习资料/课程"),
  folder("folder-camera", "folder-course", "摄影", "学习资料/课程/摄影"),
  folder("folder-inbox-course", "folder-inbox", "课程", "收件箱/课程"),
  folder("folder-readonly", "0", "共享资料", "共享资料", false),
];

export function demoQuarkFolderPage(query: URLSearchParams): QuarkFolderPage {
  if (query.get("accountId") !== "q-main") throw Object.assign(new Error("夸克账号不存在"), { code: "NOT_FOUND" });
  const parentId = query.get("parentId") ?? "0";
  const parent = quarkFolderFixtures.find((node) => node.id === parentId);
  if (!parent) throw Object.assign(new Error("文件夹已不存在，请返回上级重新选择"), { code: "NOT_FOUND" });
  const breadcrumbs: QuarkFolder[] = [];
  let ancestor: QuarkFolder | undefined = parent;
  while (ancestor) {
    breadcrumbs.unshift(ancestor);
    ancestor = quarkFolderFixtures.find((node) => node.id === ancestor!.parentId);
  }
  const children = quarkFolderFixtures.filter((node) => node.parentId === parentId);
  const offset = Number(query.get("cursor") || 0);
  const count = Number(query.get("pageSize") || 50);
  return { items: children.slice(offset, offset + count), nextCursor: offset + count < children.length ? String(offset + count) : null, total: children.length, parent, breadcrumbs, status: "READY", taskId: null };
}

export function validateDemoQuarkTarget(id: string, revision: string): QuarkFolder {
  const target = quarkFolderFixtures.find((node) => node.id === id);
  if (!target) throw Object.assign(new Error("目标文件夹不存在，请重新选择"), { code: "NOT_FOUND" });
  if (!target.selectable || target.availability !== "AVAILABLE") throw Object.assign(new Error("目标文件夹没有写入权限"), { code: "ACCOUNT_PERMISSION_DENIED" });
  if (target.revision !== revision) throw Object.assign(new Error("目标文件夹已变化，请重新选择"), { code: "VERSION_CHANGED" });
  return target;
}
