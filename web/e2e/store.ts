// The logbook store the specs run over (those-who-run/paddock#130, the maintainer's
// decision D8): web/e2e/store/data, made by web/e2e/store/make.go from v1's
// recordings replayed through ecu, which store.json describes. The pinned logbook
// image serves it in CI; no ecu runs there.
//
// Each recording is a source of its own, and each pit wall the global setup starts
// shows the sources of one of v1's stores (STORES), so one logbook stands in for
// all of them.
//
// The live session page (#298 AC5): the store holds the pit stop's recording once
// more as an open session, its first LIVE_LAPS_AT_START laps stored and ecu never
// having ended it. `addLap` publishes what ecu published for its next lap
// (store/live-lap.jsonl.gz) to the run's NATS, and logbook stores it as it would a
// live lap: the next poll sees it.
import { readFileSync } from "node:fs";

import { publishCaptured, readCaptured } from "./nats";

export const STORE_DIR = new URL("./store/", import.meta.url);

/** store.json, as web/e2e/store/make.go writes it. */
export interface StoreManifest {
  sessions: Record<string, string>;
  live_laps_at_start: number;
  events_seq: number;
  frames_seq: number;
  ecu_at_the_end: { src: string; status: Record<string, unknown>; session: Record<string, unknown> | null };
}

export const MANIFEST = JSON.parse(readFileSync(new URL("store.json", STORE_DIR), "utf8")) as StoreManifest;

/** Each source's session id. */
export const SESSION = (src: string): string => {
  const id = MANIFEST.sessions[src];
  if (!id) {
    throw new Error(`store.json has no session for ${src}`);
  }
  return id;
};

/** The pit walls' stores, by the sources each shows (TWR_PITWALL_SRC), as v1's
 * global setup made them: the main store (the joined race, the time trial, the
 * race, the pit stop and the open session); the time trial and a session with no
 * full lap; lap compare's (the race, the pit stop and the no-stop race); and an
 * empty one, a source with no sessions. */
export const STORES = {
  main: ["replay-e2e-joined", "replay-e2e-fuji", "replay-e2e-gilles", "replay-e2e-pit-stop", "replay-e2e-live"],
  short: ["replay-e2e-fuji", "replay-e2e-zero-lap"],
  compare: ["replay-e2e-gilles", "replay-e2e-pit-stop", "replay-e2e-no-stop"],
  empty: ["replay-e2e-none"],
} as const;

/** The open session's id. */
export const LIVE_SESSION_ID = SESSION("replay-e2e-live");

/** Laps the open session starts with; addLap adds the next. */
export const LIVE_LAPS_AT_START = MANIFEST.live_laps_at_start;

/** Commits the open session's next lap, as ecu publishes it when the lap closes. */
export async function addLap(): Promise<void> {
  await publishCaptured(readCaptured(new URL("live-lap.jsonl.gz", STORE_DIR)));
}
