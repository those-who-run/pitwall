// The pit wall's client and formatter cases from contracts, at the tag
// buf.gen.yaml pins (those-who-run/paddock#127, #128): `buf generate` writes
// protobuf-es v2's TypeScript for twr.pitwall.v1, and format/format-cases.json
// comes from the same tag, so format.test.ts checks the cases the Go formatter
// checks. Both land in src/api/gen/, which is committed.
//
//   node scripts/contracts.ts           write src/api/gen/
//   node scripts/contracts.ts --check   fail when src/api/gen/ is not what the tag gives
//
// Both need the network (GitHub). Run from web/.
import { spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync, cpSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, relative } from "node:path";

const GEN = join("src", "api", "gen");
const REPO = "those-who-run/contracts";

/** The contracts tag buf.gen.yaml pins. */
export function pinnedTag(template: string): string {
  const tag = /^\s*tag:\s*(v\d+\.\d+\.\d+)\s*$/m.exec(template)?.[1];
  if (!tag) {
    throw new Error("buf.gen.yaml pins no contracts tag (tag: vX.Y.Z)");
  }
  return tag;
}

/** Every file under dir, relative to it, sorted. */
export function files(dir: string): string[] {
  if (!existsSync(dir)) {
    return [];
  }
  const out: string[] = [];
  const walk = (d: string) => {
    for (const e of readdirSync(d, { withFileTypes: true })) {
      const path = join(d, e.name);
      if (e.isDirectory()) {
        walk(path);
      } else {
        out.push(relative(dir, path));
      }
    }
  };
  walk(dir);
  return out.sort();
}

/** The files that differ between two trees: added, removed or changed. */
export function differences(want: string, have: string): string[] {
  const a = files(want);
  const b = files(have);
  const all = [...new Set([...a, ...b])].sort();
  return all.filter((f) => {
    if (!a.includes(f) || !b.includes(f)) {
      return true;
    }
    return !readFileSync(join(want, f)).equals(readFileSync(join(have, f)));
  });
}

async function generate(out: string): Promise<void> {
  const buf = spawnSync("buf", ["generate", "--template", "buf.gen.yaml", "-o", out], {
    stdio: "inherit",
    shell: false,
    env: { ...process.env, PATH: `${join("node_modules", ".bin")}:${process.env.PATH ?? ""}` },
  });
  if (buf.status !== 0) {
    throw new Error(`buf generate failed (${buf.status ?? buf.signal})`);
  }
  const tag = pinnedTag(readFileSync("buf.gen.yaml", "utf8"));
  const url = `https://raw.githubusercontent.com/${REPO}/${tag}/format/format-cases.json`;
  const resp = await fetch(url);
  if (!resp.ok) {
    throw new Error(`GET ${url}: ${resp.status}`);
  }
  const dir = join(out, GEN);
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, "format-cases.json"), Buffer.from(await resp.arrayBuffer()));
}

async function main(check: boolean): Promise<number> {
  const tmp = mkdtempSync(join(tmpdir(), "pitwall-contracts-"));
  try {
    await generate(tmp);
    const fresh = join(tmp, GEN);
    if (check) {
      const stale = differences(fresh, GEN);
      if (stale.length > 0) {
        console.error(`src/api/gen/ is not contracts' at the pinned tag (npm run api): ${stale.join(", ")}`);
        return 1;
      }
      return 0;
    }
    rmSync(GEN, { recursive: true, force: true });
    cpSync(fresh, GEN, { recursive: true });
    return 0;
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
}

if (import.meta.main) {
  process.exitCode = await main(process.argv.includes("--check"));
}
