import { createApp } from "vue";
import ElementPlus from "element-plus";
import "element-plus/dist/index.css";
import "../../src/style.css";
import ArchiveSettingsHost from "./ArchiveSettingsHost.vue";
createApp(ArchiveSettingsHost).use(ElementPlus).mount("#app");
