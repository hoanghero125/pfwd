import { defineConfig, devices } from "@playwright/test";

// Two projects:
// - mock: the built app (vite preview) with the daemon's API and event
//   stream mocked in the browser. It needs no pfwd, so it runs anywhere.
// - real: the app served by a real pfwd ui; test/e2e's TestE2EWebUI runs
//   it with PFWD_UI_URL set.
// PW_CHANNEL=chrome uses the installed Chrome instead of Playwright's.
const channel = process.env.PW_CHANNEL || undefined;

export default defineConfig({
  testDir: "e2e",
  timeout: 30_000,
  fullyParallel: true,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: { ...devices["Desktop Chrome"], channel, trace: "retain-on-failure" },
  projects: [
    { name: "mock", testMatch: "ui.spec.ts", use: { baseURL: "http://127.0.0.1:4173" } },
    { name: "real", testMatch: "real.spec.ts" },
  ],
  webServer: process.env.PFWD_UI_URL
    ? undefined
    : { command: "npm run preview", url: "http://127.0.0.1:4173", reuseExistingServer: !process.env.CI },
});
