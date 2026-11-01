// @vitest-environment node
// #128 AC3: a case changed in contracts fails format.test.ts. The cases reach
// pitwall one way: `npm run api` takes format/format-cases.json from the contracts
// tag buf.gen.yaml pins (scripts/contracts.test.ts, takeFiles), and `api:check`
// fails on a copy that differs from the tag's. So a tag that changes a case's
// expected text is what this runs: format.test.ts, in a Vitest of its own, over a
// copy of the cases with one `out` changed, which must fail; and over an unchanged
// copy, which must pass, so the failure is the change's and not the set-up's.
import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { afterAll, describe, expect, it } from "vitest";

// Vitest runs in web/, as `npm test` and make web-check start it.
const WEB = process.cwd();
const CASES = join(WEB, "src", "api", "gen", "format-cases.json");
const tmp = mkdtempSync(join(tmpdir(), "format-cases-"));

afterAll(() => rmSync(tmp, { recursive: true, force: true }));

/** format.test.ts run over `cases`, in a Vitest of its own: its exit code and output. */
function formatTestOver(name: string, cases: string): { status: number | null; output: string } {
  const path = join(tmp, name);
  writeFileSync(path, cases);
  const run = spawnSync(
    process.execPath,
    [
      join(WEB, "node_modules", "vitest", "vitest.mjs"),
      "run",
      "--config",
      "scripts/format-cases.vitest.config.ts",
      "src/lib/format.test.ts",
    ],
    { cwd: WEB, env: { ...process.env, FORMAT_CASES: path, CI: "1" }, encoding: "utf8", timeout: 120_000 },
  );
  return { status: run.status, output: `${run.stdout}${run.stderr}` };
}

describe("a case changed in contracts (#128 AC3)", () => {
  const original = readFileSync(CASES, "utf8");

  it("passes over the cases as they are", () => {
    const run = formatTestOver("same.json", original);
    expect(run.output).toMatch(/Test Files\s+1 passed/);
    expect(run.status, run.output).toBe(0);
  }, 120_000);

  it("fails over a copy with one case's expected text changed", () => {
    const changed = original.replace('"out": "49.677"', '"out": "49.678"');
    expect(changed).not.toBe(original);
    const run = formatTestOver("changed.json", changed);
    expect(run.status, run.output).not.toBe(0);
    // The failure names the changed case: lapTime(49677).
    expect(run.output).toContain("lapTime(49677)");
    expect(run.output).toMatch(/1 failed/);
  }, 120_000);
});
