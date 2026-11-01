// Starts v2's pit wall over a logbook store, serving web/dist (ADR 0003 point 5;
// those-who-run/paddock#130). Specs get its address as PITWALL_URL, through
// ./pitwall.ts.
//
// The maintainer's decision D8 (2026-10-31): NATS and the pinned logbook image over
// a committed data directory, and no ecu. web/e2e/store/data was made from v1's
// recordings replayed through ecu (web/e2e/store/make.go); the set-up copies it,
// starts NATS with paddock's streams and live bucket, starts logbook on the copy,
// then starts the Go API (`go build ./cmd/pitwall`, or PITWALL_BIN) once for each
// of v1's stores, each showing that store's sources (./store.ts, STORES):
//
// - the main store, PITWALL_URL: the joined race, the time trial, the race, the
//   pit stop, and the pit stop again as a session still open (#298 AC5);
// - beside it (#297), reached through `storeUrl(name)`: one with a session of no
//   full lap, an empty one, and lap compare's (#299), whose no-stop race is
//   missing lap MISSING_LAP's file, deleted from the copy here; and one whose
//   logbook can't be reached, v1's "index can't be read": a pit wall on a NATS
//   address nothing listens on, whose data calls answer unavailable.
//
// The engineer is the live bucket (./engineer.ts). E2E_CONTAINER names the
// container runtime (docker unless set; podman works as well).
//
// E2E_NO_LIVE_SESSION=1 leaves the open session out of the main pit wall, so no
// card says "Running" under an "Idle" chip: the docs' screenshots set it
// (playwright.docs.config.ts). Their fixed clock needs nothing here: the store's
// sessions are dated by web/e2e/store/make.go, and the browser's clock is fixed
// in ./pitwall.ts.
import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { cpSync, existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { gunzipSync } from "node:zlib";

import { bootstrap, closeNats } from "./nats";
import { MANIFEST, SESSION, STORE_DIR, STORES } from "./store";

const WEB = resolve(fileURLToPath(new URL("..", import.meta.url)));
const ROOT = resolve(WEB, "..");

/** The images, pinned by digest (paddock#130 R5): NATS as paddock's compose runs
 * it, and the logbook web/e2e/store/make.go made the store with. Renovate bumps
 * them (renovate.json). */
export const NATS_IMAGE =
  "docker.io/library/nats:2.15.1-alpine@sha256:7ae34c25dfd6fe2e01c6483eabef20d796690fdcac6c4d94ca780a6cf9877fde";
export const LOGBOOK_IMAGE =
  "ghcr.io/those-who-run/twr-logbook:0.1.7@sha256:b4fe71257ed0a122cb6942072c1dcff8192c2ae0fd345b4a13b8ff852d85ffd0";

/** The lap whose Parquet file the set-up deletes from the no-stop race in the copy,
 * so a spec sees a missing lap file (#299 AC5). The session's id and the lap are in
 * process.env.E2E_MISSING_LAP, as `<session id>/<lap>`. */
export const MISSING_LAP = 6;

/** Sessions in the main store. */
export const STORE_SESSIONS = STORES.main.length;

/** The main pit wall's sources: STORES.main, without the open session when
 * E2E_NO_LIVE_SESSION is 1. */
export function mainSources(env: Record<string, string | undefined> = process.env): readonly string[] {
  return env.E2E_NO_LIVE_SESSION === "1" ? STORES.main.filter((s) => s !== "replay-e2e-live") : STORES.main;
}

/** The other pit walls (#297 AC2, AC6, #299). */
export type OtherStore = "short" | "empty" | "broken" | "compare";

const container = () => process.env.E2E_CONTAINER ?? "docker";

function freePort(): Promise<number> {
  return new Promise((ok, fail) => {
    const server = createServer();
    server.unref();
    server.on("error", fail);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      server.close(() => (typeof address === "object" && address ? ok(address.port) : fail()));
    });
  });
}

async function waitFor(what: string, ms: number, ready: () => Promise<boolean>): Promise<void> {
  const until = Date.now() + ms;
  for (;;) {
    try {
      if (await ready()) {
        return;
      }
    } catch {
      // not answering yet
    }
    if (Date.now() > until) {
      throw new Error(`${what} was not ready within ${ms} ms`);
    }
    await new Promise((r) => setTimeout(r, 200));
  }
}

/** Runs a container detached, on the host's network, and returns its name. */
function runContainer(name: string, args: string[]): string {
  execFileSync(container(), ["run", "--detach", "--rm", "--name", name, "--network", "host", ...args], {
    stdio: ["ignore", "ignore", "inherit"],
  });
  return name;
}

function stopContainer(name: string): void {
  try {
    if (process.env.E2E_LOGS) {
      execFileSync(container(), ["logs", name], { stdio: "inherit" });
    }
    execFileSync(container(), ["stop", "--time", "5", name], { stdio: "ignore" });
  } catch {
    // already gone
  }
}

/** The store's copy in `dir`: the index unzipped, and the no-stop race's lap
 * MISSING_LAP's file gone. */
function copyStore(dir: string): void {
  cpSync(new URL("data", STORE_DIR), dir, { recursive: true });
  const gz = join(dir, "index.duckdb.gz");
  writeFileSync(join(dir, "index.duckdb"), gunzipSync(readFileSync(gz)));
  rmSync(gz);
  const noStop = SESSION("replay-e2e-no-stop");
  const lap = join(dir, "sessions", "replay-e2e-no-stop", noStop, `lap-${String(MISSING_LAP).padStart(3, "0")}.parquet`);
  if (!existsSync(lap)) {
    throw new Error(`the store has no ${lap} to delete`);
  }
  rmSync(lap);
  process.env.E2E_MISSING_LAP = `${noStop}/${MISSING_LAP}`;
}

/** The Go API, built from this checkout unless PITWALL_BIN names one. */
function pitwallBinary(tmp: string): string {
  if (process.env.PITWALL_BIN) {
    return process.env.PITWALL_BIN;
  }
  const bin = join(tmp, "pitwall");
  execFileSync("go", ["build", "-o", bin, "./cmd/pitwall"], { cwd: ROOT, stdio: "inherit" });
  return bin;
}

/** A pit wall showing `sources`, on NATS at `natsUrl`, serving web/dist. */
async function startPitwall(
  bin: string,
  natsUrl: string,
  sources: readonly string[],
): Promise<{ url: string; process: ChildProcess }> {
  const port = await freePort();
  const url = `http://127.0.0.1:${port}`;
  const child = spawn(bin, [], {
    env: {
      ...process.env,
      ADDR: `127.0.0.1:${port}`,
      LOG_LEVEL: "warn",
      TWR_NATS_URL: natsUrl,
      TWR_PITWALL_SRC: sources.join(","),
      TWR_PITWALL_STATIC_DIR: join(WEB, "dist"),
      // The engineer's values are written once per test, not every second as ecu
      // does: a value is never stale within a run.
      TWR_PITWALL_STALE: "1h",
    },
    stdio: ["ignore", "inherit", "inherit"],
  });
  await waitFor(`the pit wall on ${url}`, 15_000, async () => (await fetch(`${url}/healthz`)).ok);
  return { url, process: child };
}

export default async function globalSetup(): Promise<() => Promise<void>> {
  if (!existsSync(join(WEB, "dist", "index.html"))) {
    throw new Error("web/dist has no index.html: run make web-build first");
  }
  const tmp = mkdtempSync(join(tmpdir(), "twr-e2e-"));
  const data = join(tmp, "data");
  copyStore(data);
  const bin = pitwallBinary(tmp);
  const run = `twr-e2e-${process.pid}`;
  const containers: string[] = [];
  const pitwalls: ChildProcess[] = [];
  const stop = async () => {
    for (const p of pitwalls) {
      p.kill("SIGTERM");
    }
    await closeNats();
    for (const name of containers.reverse()) {
      stopContainer(name);
    }
    rmSync(tmp, { recursive: true, force: true });
  };

  try {
    const natsPort = await freePort();
    const natsUrl = `nats://127.0.0.1:${natsPort}`;
    containers.push(runContainer(`${run}-nats`, [NATS_IMAGE, "-js", "-a", "127.0.0.1", "-p", String(natsPort)]));
    process.env.E2E_NATS_URL = natsUrl;
    await waitFor("NATS", 30_000, async () => {
      await bootstrap({ events: MANIFEST.events_seq, frames: MANIFEST.frames_seq });
      return true;
    });

    // logbook as the image's user would be: here, the runner's, so the copy it
    // writes to is the runner's.
    const healthPort = await freePort();
    const uid = process.getuid?.() ?? 1000;
    const gid = process.getgid?.() ?? 1000;
    const podman = container() === "podman";
    containers.push(
      runContainer(`${run}-logbook`, [
        "--user",
        `${uid}:${gid}`,
        ...(podman ? ["--userns", "keep-id"] : []),
        "--volume",
        `${data}:/data${podman ? ":z" : ""}`,
        "--env",
        "TWR_DATA_DIR=/data",
        "--env",
        `TWR_NATS_URL=${natsUrl}`,
        "--env",
        `TWR_HEALTH_ADDR=127.0.0.1:${healthPort}`,
        LOGBOOK_IMAGE,
      ]),
    );
    await waitFor("logbook", 60_000, async () => (await fetch(`http://127.0.0.1:${healthPort}/healthz`)).ok);

    const main = await startPitwall(bin, natsUrl, mainSources());
    pitwalls.push(main.process);
    // logbook answers once its services are up: the main pit wall's /healthz says.
    await waitFor("logbook's services", 30_000, async () => {
      const h = (await (await fetch(`${main.url}/healthz`)).json()) as { logbook: boolean };
      return h.logbook;
    });
    const others: Record<string, string> = {};
    for (const name of ["short", "empty", "compare"] as const) {
      const p = await startPitwall(bin, natsUrl, STORES[name]);
      pitwalls.push(p.process);
      others[name] = p.url;
    }
    // Nothing listens there: the data calls answer unavailable.
    const broken = await startPitwall(bin, `nats://127.0.0.1:${await freePort()}`, STORES.main);
    pitwalls.push(broken.process);
    others.broken = broken.url;

    const version = ((await (await fetch(`${main.url}/healthz`)).json()) as { version: string }).version;
    process.env.E2E_PITWALL_VERSION = version;
    process.env.PITWALL_URL = main.url;
    process.env.PITWALL_STORES = JSON.stringify(others);
  } catch (err) {
    await stop();
    throw err;
  }
  // The set-up's own connection; each worker makes its own.
  await closeNats();
  return stop;
}
