<script setup lang="ts">
import { ref } from "vue";
import BilibiliDetail from "../../src/components/BilibiliDetail.vue";
import DownloadPreview from "../../src/components/DownloadPreview.vue";
import type { Selection } from "../../src/api/types";
import { mediaSelectionSession, type SavedMediaSelection } from "../../src/media-selection";
const detailOpen = ref(false), previewOpen = ref(false), contentId = ref("b1"), saved = ref("");
const selection = ref<Selection>({ mode: "EXPLICIT", resource: "BILIBILI_CONTENTS", ids: ["b1"] });
function detail(id: string) { contentId.value = id; detailOpen.value = true; }
function preview(ids = ["b1"]) { selection.value = { mode: "EXPLICIT", resource: "BILIBILI_CONTENTS", ids }; previewOpen.value = true; }
function onSaved(value: SavedMediaSelection) { saved.value = JSON.stringify(value); }
(window as any).mediaTest = { save: mediaSelectionSession.save, preview, detail };
</script>
<template><main style="padding: 32px">
  <h1>媒体组件隔离测试</h1><p>只使用演示数据，不访问真实账号或媒体。</p>
  <el-button @click="detail('b1')">单视频详情</el-button><el-button @click="detail('b2')">多 P 详情</el-button>
  <el-button @click="detail('b7')">番剧详情</el-button><el-button @click="detail('b12')">电影详情</el-button>
  <el-button @click="detail('b3')">无字幕详情</el-button>
  <el-button @click="preview()">打开待提交清单</el-button><el-button @click="preview(['b4', 'b9', 'b11'])">事实清单</el-button>
  <p data-testid="saved">{{ saved }}</p>
  <BilibiliDetail v-model="detailOpen" :content-id="contentId" @saved="onSaved" />
  <DownloadPreview v-model="previewOpen" platform="BILIBILI" :selection="selection" />
</main></template>
