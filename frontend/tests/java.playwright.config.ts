import { defineConfig } from "@playwright/test";
export default defineConfig({
  testDir: "./java-browser",
  fullyParallel: false,
  workers: 1,
  timeout: 45000,
  use: {
    baseURL: "http://127.0.0.1:5193",
    channel: "chrome",
    headless: true,
    viewport: { width: 1440, height: 900 },
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
  },
  globalSetup: "./java-browser-server.ts",
  reporter: "list",
});
