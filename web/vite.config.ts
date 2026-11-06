import { fileURLToPath } from "node:url";

import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { searchForWorkspaceRoot } from "vite";
import { defineConfig } from "vitest/config";

// `make web-dev` puts Vite in front of a running pit wall: PITWALL_URL, by default
// the one paddock's compose stack publishes on v1's port (`make pitwall` there).
const pitwall = process.env.PITWALL_URL ?? "http://127.0.0.1:9852";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
  },
  build: {
    // dist/.vite/manifest.json: what scripts/bundle-budget.ts measures.
    manifest: true,
  },
  server: {
    // Outside web/, what the tests read: the map's traces from v1's fixtures
    // and the design guide whose contrast table the tokens are checked against.
    fs: {
      allow: [
        searchForWorkspaceRoot(process.cwd()),
        fileURLToPath(new URL("../tests/fixtures", import.meta.url)),
        fileURLToPath(new URL("../docs", import.meta.url)),
      ],
    },
    proxy: { "/twr.pitwall.v1.PitwallService": pitwall, "/healthz": pitwall },
  },
  test: {
    // Component tests run in jsdom; the route import test asks for node itself.
    environment: "jsdom",
    include: ["src/**/*.test.{ts,tsx}", "scripts/**/*.test.ts"],
    restoreMocks: true,
  },
});
