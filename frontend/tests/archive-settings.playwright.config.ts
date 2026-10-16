import { defineConfig } from "@playwright/test";
export default defineConfig({
  testDir: "./archive-settings-browser", outputDir: "../test-results/archive-settings", workers: 1, fullyParallel: false, timeout: 30000,
  use: { baseURL: "http://127.0.0.1:5188", channel: "chrome", headless: true, viewport: { width: 1440, height: 900 }, screenshot: "only-on-failure", trace: "off" },
  globalSetup: "./archive-settings-browser-server.ts", reporter: "list",
});
