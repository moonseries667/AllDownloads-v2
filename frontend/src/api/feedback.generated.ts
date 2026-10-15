// Generated from contracts/openapi.yaml at d508c7a. Do not edit by hand.
// Regenerate: node frontend/src/api/generate-feedback-contract.mjs d508c7a

export type Source = {
  id: string;
  platform: "BILIBILI";
  kind: "FAVORITE" | "COLLECTION" | "USER_ADDED" | "SERIES" | "MOVIE";
  title: string;
  alias: string | null;
  url: string;
  status: string;
  lastSyncedAt: string | null;
  itemCount: number;
  revision: string;
  pinned: boolean;
  navigationGroup: "FAVORITES" | "COLLECTIONS" | "OTHER";
  displayLabel: string;
};

export type MediaTrack = {
  id: string;
  kind: "VIDEO" | "AUDIO" | "SUBTITLE" | "DANMAKU";
  available: boolean;
  reasonCode: string | null;
  language: string | null;
  languageLabel: string | null;
  subtitleOrigin: "HUMAN" | "AI" | "UNKNOWN" | "NOT_APPLICABLE";
  durationSeconds: number | null;
  sizeBytes: string | null;
};

export type MediaUnit = {
  id: string;
  contentId: string;
  externalId: string;
  title: string;
  index: number;
  kind: "MAIN" | "PART" | "EPISODE" | "LANGUAGE";
  language: string | null;
  languageLabel: string | null;
  durationSeconds: number | null;
  availability: "AVAILABLE" | "REMOVED" | "UNAVAILABLE" | "UNKNOWN";
  availabilityReason: string | null;
  tracks: Array<MediaTrack>;
};

export type StateSummary = {
  state: string;
  [key: string]: unknown;
};

export type Content = {
  id: string;
  platform: "BILIBILI";
  externalId: string;
  externalIdType: "BVID" | "SEASON_ID" | "EPISODE_ID" | "MEDIA_ID";
  kind: "SINGLE" | "MULTIPART" | "SERIES" | "MOVIE";
  revision: string;
  title: string;
  coverUrl: string | null;
  sourceIds: Array<string>;
  originalUrl: string;
  availability: "AVAILABLE" | "REMOVED" | "UNAVAILABLE" | "UNKNOWN";
  availabilityReason: string | null;
  downloadSummary: StateSummary;
  archiveSummary: StateSummary;
  updatedAt: string;
  publishedAt: string | null;
  author: null | {
  id: string;
  name: string;
};
  description: string;
  seasonId: string | null;
  sources: Array<Source>;
  mediaUnits: Array<MediaUnit>;
  warnings: Array<string>;
};

export type Target = {
  id: string;
  revision: string;
  status: "VALIDATING" | "VALID" | "INVALID";
  path: string;
  label: string;
  rootId: string | null;
  archivePolicy: "AUTO" | "NONE";
  error: Record<string, unknown> | null;
};

export type Output = {
  id: string;
  relativePath: string;
  trackIds: Array<string>;
  kind: "VIDEO" | "AUDIO" | "MUXED" | "SUBTITLE" | "DANMAKU";
  sizeBytes: string | null;
};

export type MediaSelection = {
  contentId: string;
  contentRevision: string;
  trackIds: Array<string>;
};

export type Selection = {
  mode: "EXPLICIT" | "QUERY";
  resource: "BILIBILI_CONTENTS" | "BILIBILI_PARTS";
  ids?: Array<string>;
  filters?: Record<string, string>;
  excludeIds?: Array<string>;
  mediaSelections?: Array<MediaSelection>;
};

export type PreviewRequest = {
  platform: "BILIBILI";
  selection: Selection;
  target?: {
  id: string;
  revision: string;
};
};

export type PreviewItem = {
  id: string;
  contentId: string;
  partId: string;
  title: string;
  version: string;
  relativePath: string;
  sizeBytes: string | null;
  decision: "DOWNLOAD" | "REUSE" | "BLOCKED";
  reasonCode: string;
  reason: string;
  actionLabel: string;
  selectedTrackIds: Array<string>;
  mergePolicy: "MUX" | "SEPARATE";
  outputs: Array<Output>;
  downloadState: string;
  archiveState: string;
};

export type Preview = {
  id: string;
  status: "PREPARING" | "READY" | "EXPIRED" | "NEEDS_ACTION";
  platform: "BILIBILI";
  expiresAt: string;
  storageRevision: string;
  target: Target;
  archivePolicy: "AUTO" | "NONE";
  items: Array<PreviewItem>;
  summary: {
  download: number;
  reuse: number;
  blocked: number;
  total: number;
};
  warnings: Array<string>;
};

export type Order = {
  id: string;
  previewId: string;
  status: string;
  target: Target;
  archivePolicy: "AUTO" | "NONE";
  items: Array<PreviewItem>;
  taskIds: Array<string>;
  createdAt: string;
};

export type Error = {
  code: string;
  message: string;
  retryable: boolean;
  action: string | null;
  details: Record<string, unknown>;
};

export type Runtime = {
  state: "RUNNING" | "PARTIAL" | "UNAVAILABLE" | "UNKNOWN";
  observedAt: string;
  services: Array<{
  id: string;
  required: boolean;
  state: "ACTIVE" | "LOST" | "ERROR" | "NOT_CONFIGURED" | "UNKNOWN";
  reasonCode: string | null;
  lastSeenAt: string | null;
}>;
  mount: {
  state: "MOUNTED" | "NOT_MOUNTED" | "NOT_CONFIGURED" | "UNKNOWN" | "UNSUPPORTED";
  reasonCode: string | null;
  observedAt: string | null;
};
};

export type Overview = {
  runtime: Runtime;
  bilibili: {
  accountState: string;
  refreshState: string;
  downloadCount: number;
  archiveCount: number;
};
};

export type Task = {
  id: string;
  platform?: string;
  kind: string;
  status: string;
  phase?: string;
  revision: string;
  desiredAction?: string;
  allowedActions: Array<string>;
  error?: Record<string, unknown> | null;
  createdAt?: string;
  updatedAt?: string;
  progress?: Record<string, unknown> | null;
  nextRetryAt?: string | null;
  [key: string]: unknown;
};
