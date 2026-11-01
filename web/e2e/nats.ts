// The end-to-end run's NATS (those-who-run/paddock#130): the streams and the live
// bucket paddock's bootstrap adds (./nats/*.json, copied from paddock), the live
// values the engineer and radio would write (./engineer.ts), and messages ecu
// published, sent again as it sent them (./store.ts). The specs and the global
// set-up reach the server at process.env.E2E_NATS_URL, which the set-up sets.
import { readFileSync } from "node:fs";
import { gunzipSync } from "node:zlib";

import { jetstream, jetstreamManager, type StreamConfig } from "@nats-io/jetstream";
import { Kvm, type KV } from "@nats-io/kv";
import { connect, headers, type NatsConnection } from "@nats-io/transport-node";

const DIR = new URL("./nats/", import.meta.url);

/** One message as ecu published it: logbook's replay fixtures' form. */
export interface Captured {
  subject: string;
  headers: Record<string, string[]>;
  payload: unknown;
}

let conn: Promise<NatsConnection> | null = null;

/** The connection to E2E_NATS_URL, made once per process. */
export function nats(url = process.env.E2E_NATS_URL): Promise<NatsConnection> {
  if (!url) {
    throw new Error("E2E_NATS_URL is not set: run through playwright.config.ts's global setup");
  }
  // A connection that failed is forgotten, so the set-up's wait for NATS to
  // start tries again rather than getting the same failure each time.
  conn ??= connect({ servers: url, name: "pitwall-e2e" }).catch((err: unknown) => {
    conn = null;
    throw err;
  });
  return conn;
}

/** Closes the connection, if one was made. */
export async function closeNats(): Promise<void> {
  if (conn) {
    const nc = await conn;
    conn = null;
    await nc.drain();
  }
}

function readJson<T>(name: string): T {
  return JSON.parse(readFileSync(new URL(name, DIR), "utf8")) as T;
}

/** Adds paddock's EVENTS and TELEMETRY streams and the live bucket. Each stream
 * starts after the sequence the store was made at (web/e2e/store/store.json), so
 * logbook's last-wins guard takes what the specs publish as newer. */
export async function bootstrap(firstSeq: { events: number; frames: number }): Promise<void> {
  const jsm = await jetstreamManager(await nats());
  for (const [file, first] of [
    ["events.json", firstSeq.events],
    ["telemetry.json", firstSeq.frames],
  ] as const) {
    const config = readJson<StreamConfig & { $comment?: unknown }>(file);
    delete config.$comment;
    // A test run stores a few MB: paddock's limits would ask the runner's disk
    // for GiBs it may not have.
    await jsm.streams.add({ ...config, max_bytes: 256 << 20, first_seq: first + 1 });
  }
  const live = readJson<{ bucket: string; history: number; max_value_size: number; max_bucket_size: number }>(
    "live.json",
  );
  await new Kvm(await nats()).create(live.bucket, {
    history: live.history,
    maxValueSize: live.max_value_size,
    max_bytes: live.max_bucket_size,
  });
}

let bucket: Promise<KV> | null = null;

/** The live bucket. */
export async function liveBucket(): Promise<KV> {
  bucket ??= nats().then((nc) => new Kvm(nc).open("live"));
  return bucket;
}

/** Messages from a gzipped JSON lines file of ecu's, as logbook's capture writes. */
export function readCaptured(path: string | URL): Captured[] {
  return gunzipSync(readFileSync(path))
    .toString("utf8")
    .split("\n")
    .filter((l) => l.trim() !== "")
    .map((l) => JSON.parse(l) as Captured);
}

/** Publishes msgs to JetStream as ecu sent them, headers and payload unchanged,
 * each stored before the next is sent. */
export async function publishCaptured(msgs: Captured[]): Promise<void> {
  const js = jetstream(await nats());
  for (const m of msgs) {
    const h = headers();
    for (const [k, vs] of Object.entries(m.headers ?? {})) {
      for (const v of vs) {
        h.append(k, v);
      }
    }
    await js.publish(m.subject, JSON.stringify(m.payload), { headers: h });
  }
}
