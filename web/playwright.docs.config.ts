import { defineConfig } from "@playwright/test";

import { VIEWPORTS } from "./e2e/viewports";

// The docs' screenshots (#336; those-who-run/paddock#135): `make
// docs-screenshots` runs this, and CI's docs-screenshots workflow, never `make
// web-check`. The same global setup as the e2e suite (the built app on the Go API
// over NATS and logbook's store, the engineer in the live bucket). The store's
// sessions are dated by web/e2e/store/make.go and the browser's clock is fixed
// (./e2e/pitwall.ts), so every run gives the same pictures. Writes
// docs/assets/screens/<viewport>/<page>.png.

/** No open session: the engineer chip says "Idle", so no card may say "Running". */
process.env.E2E_NO_LIVE_SESSION = "1";

const SIZES = ["phone", "desktop"];

export default defineConfig({
  testDir: "./e2e",
  testMatch: "docs.screens.ts",
  globalSetup: "./e2e/global-setup.ts",
  outputDir: "./test-results/docs",
  forbidOnly: !!process.env.CI,
  // One worker, as the e2e suite: the engineer is the live bucket every test shares.
  workers: 1,
  reporter: process.env.CI ? [["list"], ["github"]] : "list",
  use: {
    browserName: "chromium",
    timezoneId: "UTC",
    locale: "en-GB",
    reducedMotion: "reduce",
    colorScheme: "dark",
    trace: "retain-on-failure",
  },
  projects: VIEWPORTS.filter((v) => SIZES.includes(v.name)).map((v) => ({
    name: v.name,
    // The phone at device scale 2, not 3: a third of the bytes, and still sharp.
    use: v.name === "phone" ? { ...v.use, deviceScaleFactor: 2 } : v.use,
  })),
});
