import { defineConfig } from "@playwright/test";

import { VIEWPORTS } from "./e2e/viewports";

// The built app (web/dist) on the real pit wall over a logbook store, in Chromium
// at three sizes (ADR 0003 point 5; those-who-run/paddock#130). e2e/global-setup.ts
// starts NATS, logbook and the pit walls.
//
// One worker: the engineer is the live KV bucket every pit wall reads
// (e2e/engineer.ts), so a test that sets it would change what a test beside it
// sees. The browsers' clock and zone are fixed (e2e/pitwall.ts), as the store's
// sessions are dated.
export default defineConfig({
  testDir: "./e2e",
  globalSetup: "./e2e/global-setup.ts",
  outputDir: "./test-results/playwright",
  forbidOnly: !!process.env.CI,
  workers: 1,
  reporter: process.env.CI ? [["list"], ["github"]] : "list",
  use: { browserName: "chromium", trace: "retain-on-failure", timezoneId: "UTC" },
  projects: VIEWPORTS.map((v) => ({ name: v.name, use: v.use })),
});
