import { defineConfig } from "@playwright/test";
export default defineConfig({
  testDir: "./downloads-browser", outputDir: "../test-results/downloads", workers: 1, fullyParallel: false, timeout: 45000,
  use: { baseURL: "http://127.0.0.1:5197", channel: "chrome", headless: true, viewport: { width: 1440, height: 900 }, screenshot: "only-on-failure", trace: "off" },
  globalSetup: "./downloads-browser-server.ts", reporter: "list",
});
