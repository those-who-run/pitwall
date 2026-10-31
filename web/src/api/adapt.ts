// The pages' view of the API (those-who-run/paddock#127 R3): the JSON form of
// twr.pitwall.v1's generated messages (./gen, protobuf's JSON mapping, the
// types `json_types` generates), mapped to the shapes the pages were written
// against in v1, so no page changes with the client. Field names stay v1's
// snake_case; a field the JSON leaves out is null where v1 had `None`, and its
// zero value where v1 had one (protobuf's JSON omits zeros); a Timestamp is Unix
// seconds; an enum is v1's string. Every rename the contract made (contracts#41,
// "v1 field mapping") is undone here and nowhere else.
import type {
  EngineStatusJson,
  FilterOptionsJson,
  LapRowJson,
  ListLapsResponseJson,
  ListSessionsResponseJson,
  LiveSessionJson,
  SessionJson,
  TraceJson,
} from "./gen/twr/pitwall/v1/pitwall_pb";

/** Where the detector placed a session's track. */
export interface Track {
  id: number | null;
  /** "Unknown track" when the detector is not sure. */
  name: string;
  sure: boolean;
  evidence: string | null;
}

/** One session, with what its results card shows, as values. */
export interface Session {
  session_id: string;
  title: string;
  result: string;
  /** Unix seconds. */
  started_at: number;
  ended_at: number | null;
  closed: boolean;
  kind: string;
  kind_label: string;
  track: Track;
  car: string | null;
  car_code: number | null;
  finished: boolean;
  final_position: number | null;
  total_cars: number | null;
  position_shown: boolean;
  total_time_ms: number | null;
  best_lap_ms: number | null;
  best_lap_no: number | null;
  laps_completed: number;
  /** 0 for a timed session, or until the lap total is known. */
  total_laps: number;
  fuel_use: boolean | null;
  /** The fuel burnt, refuels left out (the API's fuel_burned). */
  fuel_used: number | null;
  fuel_capacity: number | null;
  fuel_by_stint: number[];
  pit_stops: number;
  driving_ms: number | null;
  paused_ms: number | null;
  joined_on_lap: number | null;
  continued: boolean;
  restart_laps: number[];
}

export interface FilterOption {
  value: string;
  count: number;
}

export interface KindOption {
  value: string;
  label: string;
  count: number;
}

export interface FilterOptions {
  track: FilterOption[];
  car: FilterOption[];
  kind: KindOption[];
}

export interface SessionList {
  sessions: Session[];
  options: FilterOptions;
  total: number;
  matched: number;
}

/** One lap in the lap table, with its flags. */
export interface LapRow {
  lap: number;
  time_ms: number | null;
  complete: boolean;
  fuel_start: number | null;
  fuel_end: number | null;
  fuel_used: number | null;
  position: number | null;
  frames: number;
  top_speed_kph: number | null;
  pit: boolean;
  out_lap: boolean;
  invalid: boolean;
  best: boolean;
  fuel_use: boolean;
  restart: boolean;
  clean: boolean;
}

export interface LapList {
  session_id: string;
  laps: LapRow[];
}

/** One lap's channels against time, one value per kept frame. */
export interface Trace {
  session_id: string;
  lap: number;
  t: number[];
  speed_kph: number[];
  throttle_pct: number[];
  brake_pct: number[];
  gear: number[];
  rpm: number[];
  x: number[];
  z: number[];
}

export interface TelemetryLink {
  connected: boolean;
  last_packet_age_s: number | null;
}

export interface TrackSeen {
  id: number | null;
  name: string | null;
  sure: boolean;
}

export interface LiveSession {
  id: string;
  kind: string;
  track: TrackSeen | null;
  car: string | null;
  lap: number | null;
  laps_total: number | null;
  position: number | null;
  cars: number | null;
}

export type EngineActivity = "idle" | "driving" | "paused" | "replay" | "demo";

/** What the engineer is doing right now. */
export interface EngineStatus {
  version: string;
  /** Unix seconds. */
  started_at: number;
  activity: EngineActivity;
  idle_secs: number;
  telemetry: TelemetryLink;
  /** null when radio says nothing of the voice: "Voice: unknown"
   * (those-who-run/paddock#125 R6). */
  voice_connected: boolean | null;
  session: LiveSession | null;
}

/** The pit wall's GET /healthz (plain HTTP, not the Connect API). */
export interface Health {
  ok: boolean;
  version: string;
  nats: boolean;
  logbook: boolean;
}

/** A double in protobuf's JSON: a number, or "NaN" and the infinities as
 * strings, which the pages can't show: null. */
type JsonDouble = number | "NaN" | "Infinity" | "-Infinity";

function num(v: JsonDouble | undefined): number | null {
  return typeof v === "number" ? v : null;
}

function nums(v: JsonDouble[] | undefined): number[] {
  return (v ?? []).map((x) => (typeof x === "number" ? x : Number.NaN));
}

const orNull = <T>(v: T | undefined): T | null => (v === undefined ? null : v);

/** A Timestamp (RFC 3339 in protobuf's JSON) as Unix seconds; null when absent
 * or unreadable. */
export function seconds(ts: string | undefined): number | null {
  if (ts === undefined) {
    return null;
  }
  const ms = Date.parse(ts);
  return Number.isNaN(ms) ? null : ms / 1000;
}

export function session(s: SessionJson): Session {
  return {
    session_id: s.sessionId ?? "",
    title: s.title ?? "",
    result: s.result ?? "",
    started_at: seconds(s.startedAt) ?? 0,
    ended_at: seconds(s.endedAt),
    closed: s.closed ?? false,
    kind: s.kind || "unknown",
    kind_label: s.kindLabel ?? "",
    track: {
      id: orNull(s.track?.trackId),
      name: s.track?.name || "Unknown track",
      sure: s.track?.sure ?? false,
      evidence: orNull(s.track?.evidence),
    },
    car: orNull(s.car),
    car_code: orNull(s.carCode),
    finished: s.finished ?? false,
    final_position: orNull(s.finalPosition),
    total_cars: orNull(s.totalCars),
    position_shown: s.positionShown ?? false,
    total_time_ms: orNull(s.totalTimeMs),
    best_lap_ms: orNull(s.bestLapMs),
    best_lap_no: orNull(s.bestLap),
    laps_completed: s.lapsCompleted ?? 0,
    total_laps: s.totalLaps ?? 0,
    fuel_use: orNull(s.fuelUse),
    fuel_used: num(s.fuelBurned),
    fuel_capacity: num(s.fuelCapacity),
    fuel_by_stint: nums(s.fuelByStint),
    pit_stops: s.pitStops ?? 0,
    driving_ms: orNull(s.drivingMs),
    paused_ms: orNull(s.pausedMs),
    joined_on_lap: orNull(s.joinedOnLap),
    continued: s.continued ?? false,
    restart_laps: [...(s.restartLaps ?? [])],
  };
}

function options(o: FilterOptionsJson | undefined): FilterOptions {
  return {
    track: (o?.track ?? []).map(({ value, count }) => ({ value: value ?? "", count: count ?? 0 })),
    car: (o?.car ?? []).map(({ value, count }) => ({ value: value ?? "", count: count ?? 0 })),
    kind: (o?.kind ?? []).map(({ value, label, count }) => ({ value: value ?? "", label: label ?? "", count: count ?? 0 })),
  };
}

export function sessionList(r: ListSessionsResponseJson): SessionList {
  return {
    sessions: (r.sessions ?? []).map(session),
    options: options(r.options),
    total: r.total ?? 0,
    matched: r.matched ?? 0,
  };
}

export function lapRow(l: LapRowJson): LapRow {
  return {
    lap: l.lap ?? 0,
    time_ms: orNull(l.timeMs),
    complete: l.complete ?? false,
    fuel_start: num(l.fuelStart),
    fuel_end: num(l.fuelEnd),
    fuel_used: num(l.fuelUsed),
    position: orNull(l.position),
    frames: l.frames ?? 0,
    top_speed_kph: num(l.topSpeedKph),
    pit: l.pit ?? false,
    out_lap: l.outLap ?? false,
    invalid: l.invalid ?? false,
    best: l.best ?? false,
    fuel_use: l.fuelUse ?? false,
    restart: l.restart ?? false,
    clean: l.clean ?? false,
  };
}

export function lapList(r: ListLapsResponseJson): LapList {
  return { session_id: r.sessionId ?? "", laps: (r.laps ?? []).map(lapRow) };
}

/** A trace's channels as plain number arrays: lap compare and the map index
 * them directly. */
export function trace(t: TraceJson | undefined, sessionId: string, lap: number): Trace {
  return {
    session_id: t?.sessionId ?? sessionId,
    lap: t?.lap ?? lap,
    t: nums(t?.t),
    speed_kph: nums(t?.speedKph),
    throttle_pct: nums(t?.throttlePct),
    brake_pct: nums(t?.brakePct),
    gear: [...(t?.gear ?? [])],
    rpm: nums(t?.rpm),
    x: nums(t?.x),
    z: nums(t?.z),
  };
}

function liveSession(s: LiveSessionJson): LiveSession {
  return {
    id: s.sessionId ?? "",
    kind: s.kind || "unknown",
    track: s.track ? { id: orNull(s.track.trackId), name: orNull(s.track.name), sure: s.track.sure ?? false } : null,
    car: orNull(s.car),
    lap: orNull(s.lap),
    laps_total: orNull(s.totalLaps),
    position: orNull(s.position),
    cars: orNull(s.totalCars),
  };
}

const activities: Partial<Record<string, EngineActivity>> = {
  ACTIVITY_IDLE: "idle",
  ACTIVITY_DRIVING: "driving",
  ACTIVITY_PAUSED: "paused",
  ACTIVITY_REPLAY: "replay",
  ACTIVITY_DEMO: "demo",
};

/** The engineer's status, or null when the pit wall says it is offline
 * (ACTIVITY_OFFLINE: none yet, stale, or NATS down) or sent none. */
export function engineStatus(s: EngineStatusJson | undefined): EngineStatus | null {
  const activity = s?.activity === undefined ? undefined : activities[s.activity];
  if (s === undefined || activity === undefined) {
    return null;
  }
  return {
    version: s.version ?? "",
    started_at: seconds(s.startedAt) ?? 0,
    activity,
    idle_secs: num(s.idleSecs) ?? 0,
    telemetry: {
      connected: s.telemetry?.connected ?? false,
      last_packet_age_s: num(s.telemetry?.lastPacketAgeSecs),
    },
    voice_connected: orNull(s.voiceConnected),
    session: s.session ? liveSession(s.session) : null,
  };
}
