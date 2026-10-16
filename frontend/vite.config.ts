import { defineConfig, loadEnv } from "vite";
import vue from "@vitejs/plugin-vue";
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  const apiMode = env.VITE_API_MODE || (mode === "demo" ? "demo" : "http");
  const apiBase = env.VITE_API_BASE || "/api/v1";
  if (!["http", "demo"].includes(apiMode)) throw new Error("VITE_API_MODE must be http or demo");
  if (apiMode === "demo" && mode !== "demo" && mode !== "test")
    throw new Error("Demo data requires explicit --mode demo; normal builds must use the Java HTTP API");
  const proxy = env.DEV_JAVA_PROXY
    ? { "/api": { target: env.DEV_JAVA_PROXY, changeOrigin: true } }
    : undefined;
  return {
    plugins: [vue(), {
      name: "alldownloads-release-build",
      apply: "build",
      generateBundle() {
        this.emitFile({ type: "asset", fileName: "release-build.json", source: JSON.stringify({ apiMode, apiBase }, null, 2) + "\n" });
      },
    }],
    define: {
      "import.meta.env.VITE_API_MODE": JSON.stringify(apiMode),
      "import.meta.env.VITE_API_BASE": JSON.stringify(apiBase),
    },
    server: {
      proxy,
    },
    preview: { proxy },
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
