<script setup lang="ts">
import { ref } from "vue";
import BilibiliDetail from "../../src/components/BilibiliDetail.vue";
const detailOpen = ref(false);
</script>
<template><main class="test-main"><p class="test-notice">隔离测试数据；不连接真实平台或下载媒体。</p><nav><router-link to="/bilibili">Bilibili 测试入口</router-link> · <router-link to="/downloads">下载测试入口</router-link> · <button @click="detailOpen = true">新版详情测试入口</button></nav><router-view v-slot="{ Component }"><keep-alive><component :is="Component" /></keep-alive></router-view><BilibiliDetail v-model="detailOpen" content-id="b1" /></main></template>
<style scoped>.test-main { padding: 24px; max-width: 100%; box-sizing: border-box; }.test-notice { color: var(--muted); }nav { margin-bottom: 24px; }</style>
