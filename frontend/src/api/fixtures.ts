import type { RecordData } from "./types";
import { createManagementSeed } from "./demo-archive-settings";
import { seedMedia } from "./demo-bilibili";
const date = "2026-10-15T08:30:00Z";
const ds = (state = "NOT_REQUESTED", done = 0, total = 1) => ({
  state,
  completedCount: done,
  totalCount: total,
  orderIds: [],
});
const ars = (state = "NOT_REQUESTED") => ({
  state,
  verifiedCount: state === "VERIFIED" ? 3 : 0,
  totalCount: 3,
  archiveIds: [],
});
export function createSeed() {
  const names = [
    "把镜头交给山野：一场秋日徒步",
    "从零理解计算机网络",
    "城市之间，记录生活的微小瞬间",
    "设计的秩序：网格与排版",
    "日本旅行影像 · 京都篇",
    "用一小时看懂线性代数",
    "宇宙的尺度 · 第一季",
    "声音的风景：山间录音",
    "手冲咖啡的日常",
    "开源项目里的工程实践",
    "光与影的练习",
    "电影的语言：视听入门",
  ];
  const contents: RecordData[] = names.map((title, i) => ({
    id: "b" + (i + 1),
    platform: "BILIBILI",
    externalId: "DEMO-BV-" + (i + 1),
    title,
    coverUrl: "/covers/cover-" + (i % 6) + ".svg",
    sourceIds:
      i === 0 ? ["s1", "s2"] : i === 6 ? ["s3"] : i === 11 ? ["s5"] : i === 7 ? ["s4"] : i < 6 ? ["s1"] : ["s2"],
    originalUrl: "https://www.bilibili.com/",
    availability: i === 10 ? "UNAVAILABLE" : "AVAILABLE",
    availabilityReason: i === 10 ? "当前账号无法访问该内容" : null,
    downloadSummary: ds(
      i === 1
        ? "DOWNLOADING"
        : i === 3 || i === 8
          ? "VERIFIED"
          : i === 5
            ? "PARTIAL"
            : "NOT_REQUESTED",
      i === 3 || i === 8 ? 1 : i === 5 ? 1 : 0,
      i === 1 ? 3 : i === 6 ? 4 : i === 5 ? 2 : 1,
    ),
    archiveSummary: ars(
      i === 3 ? "NEEDS_ACTION" : i === 8 ? "VERIFIED" : "NOT_REQUESTED",
    ),
    updatedAt: date,
    author: [
      "山野来信",
      "技术笔记",
      "日常观察",
      "设计档案",
      "慢行计划",
      "数学切片",
    ][i % 6],
    durationSeconds: 385 + i * 173,
    publishedAt: "2026-09-" + String(10 + i).padStart(2, "0") + "T04:00:00Z",
    type:
      i === 6
        ? "SERIES"
        : i === 11
          ? "MOVIE"
          : i === 1 || i === 5
            ? "MULTIPART"
            : "VIDEO",
    description:
      "这是一条用于前端验收的虚构内容。可以查看来源、选择分 P 或剧集、预览下载清单，并独立查看本机与云端状态。未调用任何平台接口。",
  }));
  const parts: RecordData[] = contents.flatMap((c) =>
    Array.from({ length: c.downloadSummary.totalCount }, (_, i) => ({
      id: c.id + "-p" + (i + 1),
      contentId: c.id,
      externalId: "demo-part-" + i,
      title:
        c.downloadSummary.totalCount === 1
          ? "正片"
          : (c.type === "SERIES" ? "第 " + (i + 1) + " 集" : "P" + (i + 1)) +
            " · " +
            ["开始探索", "深入理解", "实践与回顾", "走向远方"][i],
      index: i + 1,
      kind:
        c.type === "SERIES"
          ? "EPISODE"
          : c.downloadSummary.totalCount > 1
            ? "PART"
            : "MAIN",
      durationSeconds: 400 + i * 200,
      availability: c.availability,
    })),
  );
  const attachment = (
    id: string,
    name: string,
    state: string,
    mediaType: string = "video/mp4",
  ) => ({
    id,
    messageId: "m1",
    externalId: id,
    name,
    mediaType,
    sizeBytes: "104857600",
    state,
    downloadState: state,
    artifactId: state === "VERIFIED" ? "a2" : null,
    relativePath: "Telegram/2026.10.12-09.15.30/1001/" + name,
    metadataRelativePath:
      "Telegram/2026.10.12-09.15.30/1001/" + name + ".metadata.json",
    error:
      state === "NEEDS_ACTION"
        ? { code: "NETWORK_ERROR", message: "连接中断，可补齐该附件" }
        : null,
  });
  const messages: RecordData[] = [
    {
      id: "m1",
      eligible: true, messageIds: ["1001", "1004"], groupComplete: true,
      externalId: "1001",
      sentAt: "2026-10-12T01:15:30Z",
      text: "周末山间录音与拍摄笔记。\n\n保留环境声的层次，也保留风穿过树林的停顿。\n附件包含视频和现场录音；此消息为虚构演示数据。",
      attachments: [
        attachment("att1", "山间记录.mp4", "VERIFIED"),
        attachment("att2", "树林环境声.wav", "NEEDS_ACTION", "audio/wav"),
        { ...attachment("att4", "同组山间照片.jpg", "QUEUED", "PHOTO"), messageId: "m4", relativePath: null, metadataRelativePath: null },
        { ...attachment("att5", "同组拍摄笔记.pdf", "QUEUED", "DOCUMENT"), relativePath: null, metadataRelativePath: null },
      ],
      groupId: "g1",
      sourceLinks: [],
      downloadSummary: ds("PARTIAL", 1, 4),
    },
    {
      id: "m2",
      eligible: false, messageIds: ["1002"], groupComplete: true,
      externalId: "1002",
      sentAt: "2026-10-13T06:24:10Z",
      text: "关于工作流的三条笔记\n1. 先收录信息\n2. 按需要下载\n3. 核对后再归档",
      attachments: [],
      groupId: null,
      sourceLinks: [],
      downloadSummary: ds("NOT_REQUESTED", 0, 0),
    },
    {
      id: "m3",
      eligible: true, messageIds: ["1003"], groupComplete: true,
      externalId: "1003",
      sentAt: "2026-10-14T11:05:00Z",
      text: "设计分享会的音频，留给周末慢慢听。",
      attachments: [
        {
          ...attachment("att3", "设计分享会.mp3", "QUEUED", "audio/mpeg"),
          messageId: "m3",
          relativePath: null,
          metadataRelativePath: null,
        },
      ],
      groupId: null,
      sourceLinks: [],
      downloadSummary: ds("QUEUED", 0, 1),
    },
  ];
  // Java already decides eligibility; these are contract fixtures, not browser-side filtering rules.
  messages.push(
    { id: "m-photo-only", externalId: "1005", eligible: false, messageIds: ["1005"], groupId: null, groupComplete: true, sentAt: date, text: "仅照片示例", sourceLinks: [], attachments: [{ ...attachment("att-photo-only", "照片.jpg", "NOT_REQUESTED", "PHOTO"), messageId: "m-photo-only" }], downloadSummary: ds("NOT_REQUESTED", 0, 0) },
    { id: "m-document-only", externalId: "1006", eligible: false, messageIds: ["1006"], groupId: null, groupComplete: true, sentAt: date, text: "仅普通文件示例", sourceLinks: [], attachments: [{ ...attachment("att-document-only", "资料.zip", "NOT_REQUESTED", "DOCUMENT"), messageId: "m-document-only" }], downloadSummary: ds("NOT_REQUESTED", 0, 0) },
    { id: "m-photo-document", externalId: "1007", eligible: false, messageIds: ["1007"], groupId: null, groupComplete: true, sentAt: date, text: "照片加普通文件示例", sourceLinks: [], attachments: [attachment("att-photo", "照片.jpg", "NOT_REQUESTED", "PHOTO"), attachment("att-document", "资料.pdf", "NOT_REQUESTED", "DOCUMENT")], downloadSummary: ds("NOT_REQUESTED", 0, 0) },
  );
  const resources: RecordData[] = messages
    .filter((m) => m.attachments.length)
    .map((m, i) => ({
      id: "r" + (i + 1),
      accountId: "tg-main",
      primaryMessageId: m.id,
      messageIds: [m.id],
      mediaGroupId: m.groupId,
      text: m.text,
      sourceLinks: m.sourceLinks,
      authorTags: [],
      channel: null,
      mediaCount: m.attachments.length,
      completedCount: i === 0 ? 1 : 0,
      failedCount: i === 0 ? 1 : 0,
      attachments: m.attachments,
      directoryLabel: i === 0 ? "Telegram/2026.10.12-09.15.30/1001/" : null,
      state: i === 0 ? "PARTIAL" : "QUEUED",
    }));
  const shares: RecordData[] = [
    {
      id: "q1",
      accountId: "q-main",
      externalShareId: "abcd12345678",
      title: "影像与设计资料",
      targetFolderId: "folder-learning",
      targetFolderRevision: "1",
      targetPath: "学习资料",
      shareFolderKey: "12345678",
      watchEnabled: true,
      updateState: "UPDATED",
      lastCheckedAt: date,
      nextCheckAt: "2026-10-15T09:30:00Z",
      revision: "1",
    },
    {
      id: "q2",
      accountId: "q-main",
      externalShareId: "designABcd2026",
      title: "设计公开课",
      targetFolderId: "folder-learning",
      targetFolderRevision: "1",
      targetPath: "学习资料",
      shareFolderKey: "ABcd2026",
      watchEnabled: false,
      updateState: "NEEDS_ACTION",
      lastCheckedAt: date,
      nextCheckAt: null,
      revision: "1",
      error: { message: "分享访问已失效，已转存副本保留。" },
    },
  ];
  const node = (
    id: string,
    shareId: string,
    parentId: string | null,
    name: string,
    kind: string,
    path: string,
    state = "NOT_REQUESTED",
  ): RecordData => ({
    id,
    shareId,
    parentId,
    externalId: id,
    version: "v1",
    name,
    kind,
    relativePath: path,
    sizeBytes: kind === "FOLDER" ? null : "268435456",
    modifiedAt: date,
    availability: "AVAILABLE",
    downloadSummary: ds(state),
  });
  const nodes = [
    node("q1-folder", "q1", null, "摄影课程", "FOLDER", "摄影课程"),
    node(
      "q1-file1",
      "q1",
      null,
      "设计参考手册.pdf",
      "FILE",
      "设计参考手册.pdf",
      "VERIFIED",
    ),
    node(
      "q1-file2",
      "q1",
      null,
      "素材使用说明.txt",
      "FILE",
      "素材使用说明.txt",
    ),
    node(
      "q1-sub1",
      "q1",
      "q1-folder",
      "01 光线的方向.mp4",
      "FILE",
      "摄影课程/01 光线的方向.mp4",
    ),
    node(
      "q1-sub2",
      "q1",
      "q1-folder",
      "02 构图练习.mp4",
      "FILE",
      "摄影课程/02 构图练习.mp4",
    ),
    node(
      "q2-file1",
      "q2",
      null,
      "公开课 第一期.mp4",
      "FILE",
      "公开课 第一期.mp4",
    ),
  ];
  const task = (
    id: string,
    title: string,
    platform: string,
    kind: string,
    status: string,
    phase: string,
    allowedActions: string[],
    error: unknown = null,
  ): RecordData => ({
    id,
    title,
    platform,
    kind,
    status,
    phase,
    progress: {
      doneItems: 1,
      totalItems: 3,
      doneBytes: "134217728",
      totalBytes: "536870912",
      bytesPerSecond: status === "RUNNING" ? "8388608" : null,
    },
    desiredAction: "CONTINUE",
    allowedActions,
    error,
    nextRetryAt: null,
    createdAt: date,
    updatedAt: date,
    revision: "1",
    completedSteps: ["已确认选择范围"],
    remainingSteps: ["执行当前阶段", "核对成果"],
  });
  const tasks = [
    task(
      "t1",
      "从零理解计算机网络",
      "BILIBILI",
      "DOWNLOAD",
      "RUNNING",
      "下载媒体",
      ["PAUSE", "CANCEL"],
    ),
    task(
      "t2",
      "设计的秩序：网格与排版",
      "BILIBILI",
      "UPLOAD",
      "NEEDS_ACTION",
      "上传云端",
      ["RETRY"],
      {
        code: "AUTH_REQUIRED",
        message: "Google Drive 登录已过期；本机成果已保留。",
        action: "重新登录后重试上传",
      },
    ),
    task(
      "t3",
      "设计参考手册.pdf",
      "QUARK",
      "VERIFY",
      "RUNNING",
      "核对云端文件",
      ["CANCEL"],
    ),
    task(
      "t4",
      "树林环境声.wav",
      "TELEGRAM",
      "DOWNLOAD",
      "NEEDS_ACTION",
      "下载附件",
      ["RETRY", "CANCEL"],
      {
        code: "NETWORK_ERROR",
        message: "附件传输中断；其他附件已保留。",
        action: "补齐失败附件",
      },
    ),
  ];
  const artifacts: RecordData[] = [
    {
      id: "a1",
      platform: "BILIBILI",
      contentId: "b4",
      title: names[3],
      relativeDirectory: "Bilibili/影像收藏（收藏夹）/设计的秩序〔DEMO-BV-4〕/",
      storageRevision: "1",
      localState: "VERIFIED",
      archiveState: "NEEDS_ACTION",
      warnings: ["字幕获取失败，可单独补齐"],
      files: [
        {
          relativePath: "设计的秩序.mp4",
          kind: "MEDIA",
          sizeBytes: "536870912",
          state: "VERIFIED",
        },
        {
          relativePath: "metadata.json",
          kind: "METADATA",
          sizeBytes: "4096",
          state: "VERIFIED",
        },
        {
          relativePath: "cover.jpg",
          kind: "COVER",
          sizeBytes: "204800",
          state: "VERIFIED",
        },
      ],
      archivePath: "Google Drive / AllDownloads / Bilibili",
      cloudUrl: null,
    },
    {
      id: "a2",
      platform: "TELEGRAM",
      contentId: "m1",
      title: "周末山间录音与拍摄笔记",
      relativeDirectory: "Telegram/2026.10.12-09.15.30/1001/",
      storageRevision: "1",
      localState: "PARTIAL",
      archiveState: "NOT_REQUESTED",
      warnings: ["树林环境声.wav 未完成；必要文件缺失，尚不能归档"],
      files: [
        {
          relativePath: "山间记录.mp4",
          kind: "MEDIA",
          sizeBytes: "104857600",
          state: "VERIFIED",
        },
        {
          relativePath: "山间记录.mp4.metadata.json",
          kind: "METADATA",
          sizeBytes: "2048",
          state: "VERIFIED",
        },
      ],
      archivePath: null,
      cloudUrl: null,
    },
    {
      id: "a3",
      platform: "QUARK",
      contentId: "q1-file1",
      title: "设计参考手册.pdf",
      relativeDirectory: "Quark/12345678/",
      storageRevision: "1",
      localState: "VERIFIED",
      archiveState: "VERIFYING",
      warnings: [],
      files: [
        {
          relativePath: "设计参考手册.pdf",
          kind: "FILE",
          sizeBytes: "268435456",
          state: "VERIFIED",
        },
      ],
      archivePath: "Google Drive / AllDownloads / Quark/12345678",
      cloudUrl: null,
    },
    {
      id: "a4",
      platform: "BILIBILI",
      contentId: "b9",
      title: names[8],
      relativeDirectory:
        "Bilibili/影像收藏（收藏夹）/手冲咖啡的日常〔DEMO-BV-9〕/",
      storageRevision: "1",
      localState: "VERIFIED",
      archiveState: "VERIFIED",
      warnings: [],
      files: [
        {
          relativePath: "手冲咖啡的日常.mp4",
          kind: "MEDIA",
          sizeBytes: "134217728",
          state: "VERIFIED",
        },
        {
          relativePath: "metadata.json",
          kind: "METADATA",
          sizeBytes: "4096",
          state: "VERIFIED",
        },
      ],
      archivePath: "Google Drive / AllDownloads / Bilibili",
      cloudUrl: null,
    },
  ];
  seedMedia(contents, parts);
  return {
    version: 3,
    management: createManagementSeed(),
    scenario: "normal",
    contents,
    parts,
    messages,
    resources,
    shares,
    nodes,
    tasks,
    artifacts,
    sources: [
      {
        id: "s1",
        platform: "BILIBILI",
        kind: "FAVORITE",
        title: "影像收藏",
        alias: "",
        url: "https://space.bilibili.com/1/favlist?fid=100",
        status: "READY",
        lastSyncedAt: date,
        itemCount: 6,
        revision: "1",
        pinned: true,
      },
      {
        id: "s2",
        platform: "BILIBILI",
        kind: "COLLECTION",
        title: "灵感与知识",
        alias: "",
        url: "https://space.bilibili.com/1/lists/200",
        status: "NEEDS_ACTION",
        lastSyncedAt: date,
        itemCount: 6,
        revision: "1",
        pinned: false,
      },
      {
        id: "s3",
        platform: "BILIBILI",
        kind: "SERIES",
        title: "宇宙的尺度",
        alias: "",
        url: "https://www.bilibili.com/bangumi/play/ss300",
        status: "READY",
        lastSyncedAt: date,
        itemCount: 1,
        revision: "1",
        pinned: false,
      },
      { id: "s4", platform: "BILIBILI", kind: "USER_ADDED", title: "用户添加视频", alias: "",
        url: "https://www.bilibili.com/video/BV1Demob8", status: "READY", lastSyncedAt: date, itemCount: 1, revision: "1", pinned: false },
      { id: "s5", platform: "BILIBILI", kind: "MOVIE", title: "电影", alias: "",
        url: "https://www.bilibili.com/bangumi/media/md400", status: "READY", lastSyncedAt: date, itemCount: 1, revision: "1", pinned: false },
    ],
    orders: [
      {
        id: "o1",
        title: names[1],
        platform: "BILIBILI",
        sourceId: "s1",
        state: "RUNNING",
        createdAt: date,
        completedCount: 1,
        totalCount: 3,
        taskIds: ["t1"],
        items: parts
          .filter((p) => p.contentId === "b2")
          .map((p) => ({
            title: p.title,
            relativePath:
              "Bilibili/影像收藏（收藏夹）/" +
              names[1] +
              "/" +
              p.title +
              ".mp4",
            state: "DOWNLOADING",
          })),
      },
    ],
    accounts: [
      {
        id: "b-main",
        platform: "BILIBILI",
        label: "B 站主账号",
        state: "ACTIVE",
        capabilities: ["DOWNLOAD"],
        lastCheckedAt: date,
      },
      {
        id: "tg-main",
        platform: "TELEGRAM",
        label: "Telegram 收藏账号",
        state: "ACTIVE",
        capabilities: ["SAVED_SYNC"],
        lastCheckedAt: date,
      },
      {
        id: "q-main",
        platform: "QUARK",
        label: "夸克账号",
        state: "ACTIVE",
        capabilities: ["TRANSFER", "DOWNLOAD"],
        lastCheckedAt: date,
      },
      {
        id: "g-main",
        platform: "GOOGLE_DRIVE",
        label: "Google Drive",
        state: "AUTH_REQUIRED",
        capabilities: [],
        lastCheckedAt: date,
      },
    ],
    watcher: {
      enabled: false,
      intervalSeconds: 60,
      lastCheckedAt: null,
      nextCheckAt: null,
      scanState: {
        accountId: "tg-main",
        cursorMessageId: "0",
        scannedThroughAt: null as string | null,
        initialScanCompleted: false,
        lastScanAt: null,
        activeTaskId: null,
      },
    },
    settings: {
      downloadRoot: "D:\\AllDownloadsData",
      archiveDestinationLabel: "Google Drive / AllDownloads",
      timezone: "Asia/Shanghai",
      storageRevision: "1",
      revision: "1",
      downloadConcurrency: 3,
      uploadConcurrency: 2,
    },
    previews: {} as Record<string, RecordData>,
    targets: {} as Record<string, import("./types").DownloadTarget>,
    cleanups: {} as Record<string, RecordData>,
    authSessions: {} as Record<string, RecordData>,
    acknowledged: [] as string[],
  };
}
export type DemoState = ReturnType<typeof createSeed>;
