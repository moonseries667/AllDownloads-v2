import { createApp } from "vue";
import { createRouter, createWebHistory } from "vue-router";
import ElementPlus from "element-plus";
import zhCn from "element-plus/es/locale/lang/zh-cn";
import "element-plus/dist/index.css";
import "@pureadmin/table/dist/style.css";
import "./style.css";
import App from "./App.vue";
const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: "/",
      component: () => import("./pages/OverviewPage.vue"),
      meta: { title: "总览" },
    },
    {
      path: "/bilibili",
      component: () => import("./pages/BilibiliPage.vue"),
      meta: { title: "Bilibili" },
    },
    {
      path: "/telegram",
      component: () => import("./pages/TelegramPage.vue"),
      meta: { title: "Telegram" },
    },
    {
      path: "/quark/cloud",
      component: () => import("./pages/QuarkPage.vue"),
      meta: { title: "夸克网盘" },
    },
    {
      path: "/downloads",
      component: () => import("./pages/DownloadsPage.vue"),
      meta: { title: "下载管理" },
    },
    {
      path: "/archives",
      component: () => import("./pages/ArchivesPage.vue"),
      meta: { title: "文件与归档" },
    },
    {
      path: "/tasks",
      component: () => import("./pages/TasksPage.vue"),
      meta: { title: "任务与问题" },
    },
    {
      path: "/settings",
      component: () => import("./pages/SettingsPage.vue"),
      meta: { title: "设置" },
    },
    { path: "/:pathMatch(.*)*", redirect: "/" },
  ],
});
createApp(App).use(router).use(ElementPlus, { locale: zhCn }).mount("#app");
