import { defineConfig } from "@playwright/test";
export default defineConfig({
  testDir: "./media-browser", outputDir: "../test-results/media", workers: 1, fullyParallel: false, timeout: 30000,
  use: { baseURL: "http://127.0.0.1:5186", channel: "chrome", headless: true, viewport: { width: 1440, height: 900 }, screenshot: "only-on-failure", trace: "off" },
  globalSetup: "./media-browser-server.ts", reporter: "list",
});
