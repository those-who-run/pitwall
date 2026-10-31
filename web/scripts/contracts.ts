// The pit wall's client and formatter cases from contracts, at the tag
// buf.gen.yaml pins (those-who-run/paddock#127, #128): `buf generate` writes
// protobuf-es v2's TypeScript for twr.pitwall.v1; format/format-cases.json
// comes from the same tag, so format.test.ts checks the cases the Go formatter
// checks; and examples/pitwall/v1/, the API's answers as contracts writes them,
// which adapt.test.ts reads. All land in src/api/gen/, which is committed.
//
// The tag must be the contracts version the Go API requires in ../go.mod, when
// it requires one: the page and the server read the same API.
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

/** contracts' examples of the API's answers (examples/pitwall/v1/), fetched by
 * name: a name contracts drops fails the fetch. */
export const EXAMPLES = [
  "get_engine_status_offline.json",
  "get_engine_status_response.json",
  "get_session_response.json",
  "get_trace_response.json",
  "list_laps_response.json",
  "list_sessions_response.json",
];

/** The contracts version a go.mod requires, or null when it requires none. */
export function goContracts(gomod: string): string | null {
  return /^\s*github\.com\/those-who-run\/contracts\s+(v\S+)/m.exec(gomod)?.[1] ?? null;
}

/** Why tag and go.mod's contracts disagree, or null when they don't. */
export function pinsDiffer(tag: string, gomod: string): string | null {
  const go = goContracts(gomod);
  return go !== null && go !== tag ? `buf.gen.yaml pins contracts ${tag}, go.mod requires ${go}: bump them together` : null;
}

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
  const dir = join(out, GEN);
  const take = async (path: string, to: string) => {
    const url = `https://raw.githubusercontent.com/${REPO}/${tag}/${path}`;
    const resp = await fetch(url);
    if (!resp.ok) {
      throw new Error(`GET ${url}: ${resp.status}`);
    }
    mkdirSync(join(to, ".."), { recursive: true });
    writeFileSync(to, Buffer.from(await resp.arrayBuffer()));
  };
  await take("format/format-cases.json", join(dir, "format-cases.json"));
  for (const name of EXAMPLES) {
    await take(`examples/pitwall/v1/${name}`, join(dir, "examples", name));
  }
}

async function main(check: boolean): Promise<number> {
  const differ = pinsDiffer(pinnedTag(readFileSync("buf.gen.yaml", "utf8")), readFileSync(join("..", "go.mod"), "utf8"));
  if (differ !== null) {
    console.error(differ);
    return 1;
  }
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
