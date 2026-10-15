import { createApp } from "vue";
import ElementPlus from "element-plus";
import "element-plus/dist/index.css";
import "../../src/style.css";
import MediaHost from "./MediaHost.vue";
createApp(MediaHost).use(ElementPlus).mount("#app");
