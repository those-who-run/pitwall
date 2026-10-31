// For tests only: a page's tests answer the API as v1's routes did, with
// v1-shaped bodies (the kit's samples), and these two helpers turn that into the
// Connect API the client calls (those-who-run/paddock#127 R9). `v1Url` is the v1
// route a Connect call stands for, and `toConnect` turns a v1 answer for it into
// Connect's: the generated message's JSON, or Connect's error. No page imports
// this, so the build leaves it out.
import type { EngineStatus, LapRow, Session, Trace } from "./adapt";

const SERVICE = "/twr.pitwall.v1.PitwallService/";

type Json = Record<string, unknown>;

/** The Connect method a request calls, and its input, or null for any other
 * request. */
function connectCall(request: Request): { method: string; input: Json } | null {
  const url = new URL(request.url);
  if (!url.pathname.startsWith(SERVICE)) {
    return null;
  }
  const message = url.searchParams.get("message");
  return { method: url.pathname.slice(SERVICE.length), input: message ? (JSON.parse(message) as Json) : {} };
}

/** The v1 route a request stands for: v1's path and query for a Connect call,
 * the request's own URL for anything else (/healthz). */
export function v1Url(request: Request): URL {
  const url = new URL(request.url);
  const call = connectCall(request);
  if (call === null) {
    return url;
  }
  const id = encodeURIComponent(String(call.input.sessionId ?? ""));
  const v1 = new URL(url.origin);
  const query = (pairs: [string, unknown][]) => {
    for (const [k, v] of pairs) {
      if (v !== undefined && v !== "" && v !== 0) {
        v1.searchParams.set(k, String(v));
      }
    }
  };
  switch (call.method) {
    case "ListSessions":
      v1.pathname = "/api/v1/sessions";
      query([
        ["track", call.input.track],
        ["car", call.input.car],
        ["kind", call.input.kind],
        ["from", call.input.fromDay],
        ["to", call.input.toDay],
        ["tz", call.input.timeZone],
      ]);
      break;
    case "GetSession":
      v1.pathname = `/api/v1/sessions/${id}`;
      break;
    case "ListLaps":
      v1.pathname = `/api/v1/sessions/${id}/laps`;
      break;
    case "ListSameTrack":
      v1.pathname = `/api/v1/sessions/${id}/same-track`;
      break;
    case "GetTrace":
      v1.pathname = `/api/v1/sessions/${id}/laps/${String(call.input.lap ?? 0)}/trace`;
      query([["points", call.input.points]]);
      break;
    case "GetEngineStatus":
      v1.pathname = "/api/engine/status";
      break;
    default:
      v1.pathname = url.pathname;
  }
  return v1;
}

/** Drops null and undefined, as protobuf's JSON leaves an absent field out. */
function compact(o: Json): Json {
  return Object.fromEntries(Object.entries(o).filter(([, v]) => v !== null && v !== undefined));
}

function timestamp(secs: number | null | undefined): string | undefined {
  return secs === null || secs === undefined ? undefined : new Date(secs * 1000).toISOString();
}

export function sessionJson(s: Partial<Session>): Json {
  return compact({
    sessionId: s.session_id,
    title: s.title,
    result: s.result,
    startedAt: timestamp(s.started_at),
    endedAt: timestamp(s.ended_at),
    closed: s.closed,
    kind: s.kind,
    kindLabel: s.kind_label,
    track: s.track && compact({ trackId: s.track.id, name: s.track.name, sure: s.track.sure, evidence: s.track.evidence }),
    car: s.car,
    carCode: s.car_code,
    finished: s.finished,
    finalPosition: s.final_position,
    totalCars: s.total_cars,
    positionShown: s.position_shown,
    totalTimeMs: s.total_time_ms,
    bestLapMs: s.best_lap_ms,
    bestLap: s.best_lap_no,
    lapsCompleted: s.laps_completed,
    totalLaps: s.total_laps || undefined,
    fuelUse: s.fuel_use,
    fuelBurned: s.fuel_used,
    fuelCapacity: s.fuel_capacity,
    fuelByStint: s.fuel_by_stint,
    pitStops: s.pit_stops,
    drivingMs: s.driving_ms,
    pausedMs: s.paused_ms,
    joinedOnLap: s.joined_on_lap,
    continued: s.continued,
    restartLaps: s.restart_laps,
  });
}

export function lapJson(l: Partial<LapRow>): Json {
  return compact({
    lap: l.lap,
    timeMs: l.time_ms,
    complete: l.complete,
    fuelStart: l.fuel_start,
    fuelEnd: l.fuel_end,
    fuelUsed: l.fuel_used,
    position: l.position,
    frames: l.frames,
    topSpeedKph: l.top_speed_kph,
    pit: l.pit,
    outLap: l.out_lap,
    invalid: l.invalid,
    best: l.best,
    fuelUse: l.fuel_use,
    restart: l.restart,
    clean: l.clean,
  });
}

export function traceJson(t: Partial<Trace>): Json {
  return compact({
    sessionId: t.session_id,
    lap: t.lap,
    t: t.t,
    speedKph: t.speed_kph,
    throttlePct: t.throttle_pct,
    brakePct: t.brake_pct,
    gear: t.gear,
    rpm: t.rpm,
    x: t.x,
    z: t.z,
  });
}

export function engineJson(e: Partial<EngineStatus>): Json {
  const s = e.session;
  return compact({
    version: e.version,
    startedAt: timestamp(e.started_at),
    activity: e.activity ? `ACTIVITY_${e.activity.toUpperCase()}` : undefined,
    idleSecs: e.idle_secs,
    telemetry: e.telemetry && compact({ connected: e.telemetry.connected, lastPacketAgeSecs: e.telemetry.last_packet_age_s }),
    voiceConnected: e.voice_connected,
    session:
      s &&
      compact({
        sessionId: s.id,
        kind: s.kind,
        track: s.track && compact({ trackId: s.track.id, name: s.track.name, sure: s.track.sure }),
        car: s.car,
        lap: s.lap,
        totalLaps: s.laps_total,
        position: s.position,
        totalCars: s.cars,
      }),
  });
}

/** v1's HTTP statuses as Connect's error codes. */
function code(status: number): string {
  switch (status) {
    case 400:
      return "invalid_argument";
    case 404:
      return "not_found";
    case 502:
    case 503:
      return "unavailable";
    default:
      return "internal";
  }
}

/** HTTP's status for each Connect code, as the Connect protocol sends it. */
const httpStatus: Record<string, number> = { invalid_argument: 400, not_found: 404, unavailable: 503, internal: 500 };

/** `response`, a v1 answer to `request`, as the Connect API answers. A request
 * that is not a Connect call gets `response` as it is. */
export async function toConnect(request: Request, response: Response): Promise<Response> {
  const call = connectCall(request);
  if (call === null) {
    return response;
  }
  if (!response.ok) {
    let message = response.statusText;
    try {
      message = ((await response.clone().json()) as { error?: string }).error ?? message;
    } catch {
      // not JSON: the status text it is
    }
    const c = code(response.status);
    return Response.json({ code: c, message }, { status: httpStatus[c] });
  }
  const body = (await response.json()) as Json;
  const sessions = (body.sessions as Partial<Session>[] | undefined)?.map(sessionJson);
  switch (call.method) {
    case "ListSessions":
      return Response.json(compact({ ...body, sessions }));
    case "GetSession":
      return Response.json({ session: sessionJson(body as Partial<Session>) });
    case "ListLaps":
      return Response.json(
        compact({ sessionId: body.session_id, laps: (body.laps as Partial<LapRow>[] | undefined)?.map(lapJson) }),
      );
    case "GetTrace":
      return Response.json({ trace: traceJson(body as Partial<Trace>) });
    case "ListSameTrack":
      return Response.json(compact({ sessions }));
    case "GetEngineStatus":
      return Response.json({ status: engineJson(body as Partial<EngineStatus>) });
  }
  return Response.json(body);
}

/** A fetch stand-in that answers each request with `answer`'s v1 answer, as the
 * Connect API would: `vi.spyOn(globalThis, "fetch").mockImplementation(v1Api(...))`. */
export function v1Api(answer: (request: Request) => Response | Promise<Response>) {
  return async (input: RequestInfo | URL): Promise<Response> => {
    const request = input as Request;
    return toConnect(request, await answer(request));
  };
}
