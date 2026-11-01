// The engineer for the Playwright run (#296 Fixtures; those-who-run/paddock#130 R3):
// in v2 the pit wall reads the engineer's state from the live KV bucket, where ecu
// writes its status and open session and radio its status, under the live source
// (paddock#125). A test calls `setEngineer(page, name)`, which writes the values a
// scenario stands for into the bucket, as ecu and radio would; "down" deletes them,
// as an ecu that has stopped leaves none fresh. The chip asks the pit wall every
// 5 s, and the pit wall watches the bucket, so the next ask sees the change.
//
// The bucket is one for every pit wall the global setup starts, so the specs run
// one at a time (playwright.config.ts's workers), and each test starts with the
// engineer down (./pitwall.ts).
import type { Page } from "@playwright/test";

import { liveBucket } from "./nats";
import { LIVE_SESSION_ID } from "./store";

/** The engineer's version: the pit wall's own, which the global setup puts in
 * E2E_PITWALL_VERSION once the pit wall has answered /healthz. v2 shows both
 * versions and warns of nothing when they differ (paddock#125 R7). */
export function pitwallVersion(): string {
  return process.env.E2E_PITWALL_VERSION ?? "0.0.0";
}

/** An engineer on another version than the pit wall's. */
export const OTHER_VERSION = "0.0.0-older";

export const TRACK = "Suzuka Circuit East Course";
export const SESSION_ID = "20261017-162118";

/** The keys the pit wall reads (contracts' subjects.yaml; internal/engine). */
const KEYS = { ecu: "ecu.status.live", session: "ecu.session.live", radio: "radio.status.live" };

interface Track {
  trackId?: number;
  name: string;
  sure: boolean;
}

/** ecu.session's value, twr.state.v1.LiveSession in protobuf's JSON. */
const live = (track: Track, lap: number) => ({
  sessionId: SESSION_ID,
  sessionType: "SESSION_TYPE_RACE",
  track,
  carName: "Porsche 911 GT3 RS (992) '22",
  lap,
  totalLaps: 6,
  position: 3,
  totalCars: 16,
});

/** ecu.status's fields for each scenario, twr.state.v1.EcuStatus in protobuf's
 * JSON, and its session, if one is open. */
const SCENARIOS: Record<string, { ecu: Record<string, unknown>; session?: Record<string, unknown> }> = {
  idle: { ecu: {} },
  menus: { ecu: { telemetryConnected: true, lastPacketAgeSecs: 0.2 } },
  driving: {
    ecu: { activity: "ACTIVITY_DRIVING", idle: false, idleSecs: 0, telemetryConnected: true, lastPacketAgeSecs: 0 },
    session: live({ trackId: 4, name: TRACK, sure: true }, 3),
  },
  "driving-unsure": {
    ecu: { activity: "ACTIVITY_DRIVING", idle: false, idleSecs: 0, telemetryConnected: true, lastPacketAgeSecs: 0 },
    session: live({ name: TRACK, sure: false }, 2),
  },
  // Driving the store's open session (./store.ts), for the live session page (#298).
  "live-store": {
    ecu: { activity: "ACTIVITY_DRIVING", idle: false, idleSecs: 0, telemetryConnected: true, lastPacketAgeSecs: 0 },
    session: { ...live({ trackId: 442, name: TRACK, sure: true }, 5), sessionId: LIVE_SESSION_ID },
  },
  "driving-no-session": {
    ecu: { activity: "ACTIVITY_DRIVING", idle: false, idleSecs: 0, telemetryConnected: true, lastPacketAgeSecs: 0 },
  },
  paused: {
    ecu: { activity: "ACTIVITY_PAUSED", idle: false, idleSecs: 12, telemetryConnected: true, lastPacketAgeSecs: 0.1 },
    session: live({ trackId: 4, name: TRACK, sure: true }, 4),
  },
  replay: {
    ecu: { activity: "ACTIVITY_REPLAY", idle: false, idleSecs: 0, telemetryConnected: true, lastPacketAgeSecs: 0 },
  },
  demo: { ecu: { activity: "ACTIVITY_DEMO", idle: false, idleSecs: 0, telemetryConnected: true, lastPacketAgeSecs: 0 } },
  "other-version": { ecu: { version: OTHER_VERSION } },
};

export type Scenario =
  | "idle"
  | "menus"
  | "driving"
  | "driving-unsure"
  | "live-store"
  | "driving-no-session"
  | "paused"
  | "replay"
  | "demo"
  | "other-version"
  | "down";

/** ecu's status for a scenario, as ecu writes it under the live source. */
export function ecuStatus(scenario: Exclude<Scenario, "down">): Record<string, unknown> {
  const now = new Date().toISOString();
  return {
    source: "live",
    runId: "01JE2E0000000000000000LIVE",
    version: pitwallVersion(),
    startedAt: new Date(1_792_000_000_000).toISOString(),
    activity: "ACTIVITY_IDLE",
    idle: true,
    idleSecs: 600,
    telemetryConnected: false,
    updatedAt: now,
    ...SCENARIOS[scenario]!.ecu,
  };
}

/** Writes `values` (protobuf's JSON, or null to delete) to the live keys. */
export async function writeLive(values: { ecu: unknown; session: unknown; radio: unknown }): Promise<void> {
  const kv = await liveBucket();
  for (const [name, key] of Object.entries(KEYS) as [keyof typeof KEYS, string][]) {
    const value = values[name];
    if (value === null) {
      await kv.delete(key);
    } else {
      await kv.put(key, JSON.stringify(value));
    }
  }
}

/** From now on the engineer is in `scenario` ("down" for none), for every page:
 * the bucket is shared. `page` is kept so the specs read as they did in v1. */
export async function setEngineer(_page: Page | null, scenario: Scenario): Promise<void> {
  if (scenario === "down") {
    await writeLive({ ecu: null, session: null, radio: null });
    return;
  }
  await writeLive({
    ecu: ecuStatus(scenario),
    session: SCENARIOS[scenario]!.session ?? null,
    // radio in the voice channel, as v1's engineer said.
    radio: { source: "live", sink: "RADIO_SINK_DISCORD", voiceConnected: true, updatedAt: new Date().toISOString() },
  });
}
