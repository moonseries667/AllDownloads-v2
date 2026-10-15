import { defineConfig, loadEnv } from "vite";
import vue from "@vitejs/plugin-vue";
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  return {
    plugins: [vue()],
    server: {
      proxy: env.DEV_JAVA_PROXY
        ? { "/api": { target: env.DEV_JAVA_PROXY, changeOrigin: true } }
        : undefined,
    },
    build: {
      rollupOptions: {
        output: {
          manualChunks: (id: string) =>
            id.includes("node_modules/element-plus")
              ? "element-plus"
              : undefined,
        },
      },
    },
  };
});
