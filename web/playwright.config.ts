import { defineConfig, devices } from "@playwright/test";

// E2E 测试配置：Web 控制台端到端（API 由 page.route mock，无需真实后端）。
export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? "github" : "list",
  use: {
    baseURL: process.env.E2E_BASE_URL ?? "http://localhost:5199",
    trace: "on-first-retry",
  },
  webServer: {
    command: "pnpm dev --port 5199",
    url: "http://localhost:5199",
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
