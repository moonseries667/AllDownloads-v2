// Generated from contracts/management.schema.json at baa03f5. Do not edit by hand.
// Regenerate: node frontend/src/api/generate-management-contract.mjs baa03f5

export type DriveTarget = {
  accountId: string;
  folderId: string;
  revision: string;
  name: string;
  path: string;
};

export type DriveFolder = {
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
};

export type ArtifactFile = {
  outputId: string;
  relativePath: string;
  sizeBytes: string;
  sha256: string;
  mediaKinds?: Array<string>;
  verified: true;
};

export type Summary = {
  total: number;
  completed: number;
  running: number;
  failed: number;
  cancelled: number;
  pending: number;
};

export type DownloadChild = {
  id: string;
  projectId: string;
  contentId: string;
  partId: string | null;
  attachmentId: string | null;
  nodeId: string | null;
  title: string;
  revision: string;
  status: "QUEUED" | "RUNNING" | "PAUSING" | "PAUSED" | "WAITING_RETRY" | "NEEDS_ACTION" | "SUCCEEDED" | "FAILED" | "CANCELLED" | "PARTIAL";
  desiredAction: "CONTINUE" | "PAUSE" | "CANCEL";
  allowedActions: Array<"PAUSE" | "RESUME" | "CANCEL" | "RETRY" | "RECONCILE">;
  actionReason: string | null;
  taskIds: Array<string>;
  outputs: Array<Record<string, unknown>>;
  files: Array<Record<string, unknown>>;
};

export type DownloadProject = {
  id: string;
  batchId: string;
  platform: "BILIBILI" | "TELEGRAM" | "QUARK";
  contentId: string;
  title: string;
  revision: string;
  status: "QUEUED" | "RUNNING" | "PAUSING" | "PAUSED" | "WAITING_RETRY" | "NEEDS_ACTION" | "SUCCEEDED" | "FAILED" | "CANCELLED" | "PARTIAL";
  desiredAction: "CONTINUE" | "PAUSE" | "CANCEL";
  allowedActions: Array<"PAUSE" | "RESUME" | "CANCEL" | "RETRY" | "RECONCILE">;
  actionReason: string | null;
  createdAt: string;
  target: Record<string, unknown>;
  archivePolicy: "AUTO" | "NONE";
  archiveTarget: DriveTarget | null;
  summary: Summary;
  items: Array<DownloadChild>;
};

export type ArchiveUpload = {
  id: string;
  projectId: string;
  batchId: string;
  platform: string;
  title: string;
  status: "QUEUED" | "RUNNING" | "PAUSING" | "PAUSED" | "WAITING_RETRY" | "NEEDS_ACTION" | "SUCCEEDED" | "FAILED" | "CANCELLED" | "PARTIAL";
  revision: string;
  desiredAction: string;
  allowedActions: Array<string>;
  progress: Record<string, unknown> | null;
  target: DriveTarget;
  files: Array<Record<string, unknown>>;
  taskId: string;
  createdAt: string;
  error: Record<string, unknown> | null;
};

export type DirectorySelection = {
  id: string;
  status: "PENDING" | "SELECTED" | "CANCELLED" | "UNAVAILABLE";
  targetId: string | null;
  error: Record<string, unknown> | null;
};

export type SettingsValidation = {
  id: string;
  revision: string;
  status: "VALIDATING" | "VALID" | "INVALID";
  downloadTarget: Record<string, unknown>;
  archiveDestination: DriveTarget | null;
  error: Record<string, unknown> | null;
};
