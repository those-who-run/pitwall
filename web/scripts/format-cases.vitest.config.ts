// Vitest's config with src/lib/format.test.ts reading its cases from the file
// FORMAT_CASES names instead of src/api/gen/format-cases.json: for
// src/lib/format-cases.test.ts, which runs format.test.ts over a copy with one
// case changed, as a contracts tag that changed it would bring (#128 AC3).
import { dirname } from "node:path";

import { mergeConfig } from "vitest/config";

import config from "../vite.config";

const cases = process.env.FORMAT_CASES;
if (!cases) {
  throw new Error("FORMAT_CASES names the cases file to read");
}

export default mergeConfig(config, {
  resolve: {
    alias: [{ find: /^\.\.\/api\/gen\/format-cases\.json\?raw$/, replacement: `${cases}?raw` }],
  },
  // The copy is in a temporary directory, which Vite serves only when allowed.
  server: { fs: { allow: [dirname(cases)] } },
});
