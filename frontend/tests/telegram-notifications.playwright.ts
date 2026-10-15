import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";
export default defineConfig({
  testDir: "./feedback2", testMatch: "telegram-notifications.spec.ts", workers: 1,
  timeout: 30000,
  use: { baseURL: "http://127.0.0.1:5193", channel: "chrome", headless: true,
    viewport: { width: 1440, height: 900 }, screenshot: "only-on-failure" },
  webServer: {
    cwd: fileURLToPath(new URL("..", import.meta.url)),
    command: "node node_modules/vite/bin/vite.js --host 127.0.0.1 --port 5193 --strictPort",
    url: "http://127.0.0.1:5193", reuseExistingServer: false,
    env: { VITE_API_MODE: "http" }, timeout: 30000,
  },
  reporter: "list",
});
