// The adapter (those-who-run/paddock#127 R3): a generated message, as the pit
// wall sends it, comes out as the shape the pages read, and the tests' fake API
// (./testing.ts) is its exact inverse, so the pages' tests answer what the pit
// wall would.
import { afterEach, describe, expect, it, vi } from "vitest";

import { engine, liveSession, LAPS, SESSIONS } from "@/kit/samples";

import * as adapt from "./adapt";
import { ApiError, client, health } from "./client";
import type { Session } from "./adapt";
import type { Input } from "./client";
import type {
  EngineStatusJson,
  GetEngineStatusResponseJson,
  GetSessionResponseJson,
  GetTraceResponseJson,
  LapRowJson,
  ListLapsResponseJson,
  ListSessionsResponseJson,
  SessionJson,
  TraceJson,
} from "./gen/twr/pitwall/v1/pitwall_pb";
import engineJsonExample from "./gen/examples/get_engine_status_response.json?raw";
import offlineExample from "./gen/examples/get_engine_status_offline.json?raw";
import sessionExample from "./gen/examples/get_session_response.json?raw";
import traceExample from "./gen/examples/get_trace_response.json?raw";
import lapsExample from "./gen/examples/list_laps_response.json?raw";
import listExample from "./gen/examples/list_sessions_response.json?raw";
import { engineJson, lapJson, sessionJson, toConnect, traceJson, v1Url } from "./testing";

describe("the adapter", () => {
  it.each(Object.entries(SESSIONS))("maps the %s session both ways", (_, s) => {
    expect(adapt.session(sessionJson(s) as SessionJson)).toEqual(s);
  });

  it("maps every lap both ways", () => {
    for (const lap of LAPS) {
      expect(adapt.lapRow(lapJson(lap) as LapRowJson)).toEqual(lap);
    }
  });

  it("maps the engineer's status both ways, voice unknown included", () => {
    for (const e of [
      engine(),
      engine({ activity: "driving", session: liveSession(), voice_connected: null }),
      engine({ telemetry: { connected: true, last_packet_age_s: 0.1 } }),
    ]) {
      expect(adapt.engineStatus(engineJson(e) as EngineStatusJson)).toEqual(e);
    }
  });

  it("reads ACTIVITY_OFFLINE, or no status, as no engineer", () => {
    expect(adapt.engineStatus({ activity: "ACTIVITY_OFFLINE" })).toBeNull();
    expect(adapt.engineStatus({})).toBeNull();
    expect(adapt.engineStatus(undefined)).toBeNull();
  });

  it("keeps a trace's channels as plain number arrays", () => {
    const t = { session_id: "s", lap: 2, t: [0, 0.5], speed_kph: [100, 101], throttle_pct: [1, 2], brake_pct: [0, 0], gear: [3, 3], rpm: [5000, 5100], x: [1, 2], z: [3, 4] };
    const got = adapt.trace(traceJson(t) as TraceJson, "s", 2);
    expect(got).toEqual(t);
    expect(Array.isArray(got.speed_kph)).toBe(true);
    expect(adapt.trace(undefined, "s", 2)).toEqual({ ...t, t: [], speed_kph: [], throttle_pct: [], brake_pct: [], gear: [], rpm: [], x: [], z: [] });
  });

  it("fills a list's missing options and a session's missing track", () => {
    const list = adapt.sessionList({ sessions: [{ sessionId: "s" }] });
    expect(list.options).toEqual({ track: [], car: [], kind: [] });
    expect(list.sessions[0]!.track).toEqual({ id: null, name: "Unknown track", sure: false, evidence: null });
    expect(list.sessions[0]!.kind).toBe("unknown");
    expect(list.sessions[0]!.total_laps).toBe(0);
  });
});

// contracts' own examples of the API's answers, at the pinned tag (npm run api
// takes them into src/api/gen/examples/): the Suzuka East pit-stop race.
describe("the adapter on contracts' examples", () => {
  const SUZUKA: Session = {
    session_id: "01JB9QZ4V3X8M2K7T5R1N6PDWA",
    title: "Suzuka Circuit East Course · 6-lap race",
    result: "Finished P1 of 2 in 6:29.934",
    started_at: Date.parse("2026-10-18T13:21:29.015Z") / 1000,
    ended_at: Date.parse("2026-10-18T13:28:01.176Z") / 1000,
    closed: true,
    kind: "lap_race",
    kind_label: "6-lap race",
    track: {
      id: 442,
      name: "Suzuka Circuit East Course",
      sure: true,
      evidence: "finish line crossed, a full lap fits the layout (1.00)",
    },
    car: "Honda Civic Si Group A version '87",
    car_code: 683,
    finished: true,
    final_position: 1,
    total_cars: 2,
    position_shown: true,
    total_time_ms: 389934,
    best_lap_ms: 59541,
    best_lap_no: 5,
    laps_completed: 6,
    total_laps: 6,
    fuel_use: true,
    fuel_used: 130.393,
    fuel_capacity: 100,
    fuel_by_stint: [73.825, 56.568],
    pit_stops: 1,
    driving_ms: 390108,
    paused_ms: 2052,
    joined_on_lap: null,
    continued: false,
    restart_laps: [],
  };

  it("maps GetSession's", () => {
    const got = adapt.session((JSON.parse(sessionExample) as GetSessionResponseJson).session!);
    expect({ ...got, result: SUZUKA.result }).toEqual(SUZUKA);
    expect(got.result).toMatch(/Finished P1 of 2 in 6:29\.934$/);
  });

  it("maps ListSessions', its options included", () => {
    const got = adapt.sessionList(JSON.parse(listExample) as ListSessionsResponseJson);
    expect(got.sessions.map((s) => s.session_id)).toEqual([SUZUKA.session_id]);
    expect([got.total, got.matched]).toEqual([14, 1]);
    expect(got.options.track).toEqual([
      { value: "Suzuka Circuit East Course", count: 1 },
      { value: "Unknown track", count: 0 },
    ]);
    expect(got.options.kind[1]).toEqual({ value: "ranked", label: "qualifying or practice", count: 0 });
  });

  it("maps ListLaps', with the pit and out laps' flags", () => {
    const got = adapt.lapList(JSON.parse(lapsExample) as ListLapsResponseJson);
    expect(got.session_id).toBe(SUZUKA.session_id);
    expect(got.laps.map((l) => [l.lap, l.time_ms, l.pit, l.out_lap, l.clean, l.fuel_use, l.position])).toEqual([
      [3, 82544, true, false, false, true, 1],
      [4, 69892, false, true, false, true, 1],
    ]);
    expect(got.laps[1]!.fuel_used).toBe(-58.577);
  });

  it("maps GetTrace's channels to plain arrays of one length", () => {
    const t = adapt.trace((JSON.parse(traceExample) as GetTraceResponseJson).trace, "", 0);
    expect([t.session_id, t.lap]).toEqual([SUZUKA.session_id, 5]);
    const lengths = new Set([t.t, t.speed_kph, t.throttle_pct, t.brake_pct, t.gear, t.rpm, t.x, t.z].map((c) => c.length));
    expect([...lengths]).toEqual([t.t.length]);
    expect(t.speed_kph[2]).toBe(63.2);
  });

  it("maps GetEngineStatus', and its offline answer to no engineer", () => {
    const got = adapt.engineStatus((JSON.parse(engineJsonExample) as GetEngineStatusResponseJson).status);
    expect(got).toEqual({
      version: "0.1.14",
      started_at: Date.parse("2026-10-18T09:02:41Z") / 1000,
      activity: "driving",
      idle_secs: 0,
      telemetry: { connected: true, last_packet_age_s: 0 },
      voice_connected: true,
      session: {
        id: SUZUKA.session_id,
        kind: "lap_race",
        track: { id: 442, name: "Suzuka Circuit East Course", sure: true },
        car: "Honda Civic Si Group A version '87",
        lap: 3,
        laps_total: 6,
        position: 1,
        cars: 2,
      },
    });
    expect(adapt.engineStatus((JSON.parse(offlineExample) as GetEngineStatusResponseJson).status)).toBeNull();
  });
});

describe("the client (R1, R6)", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  const answer = (response: Response) => vi.spyOn(globalThis, "fetch").mockResolvedValue(response);
  const signal = () => new AbortController().signal;

  it("is typed by the generated service: a method it doesn't have fails tsc", () => {
    // @ts-expect-error no such method in PitwallService
    const missing: Input<"listSession"> = {};
    const known: Input<"getTrace"> = { sessionId: "s", lap: 1, points: 10 };
    expect([missing, known]).toHaveLength(2);
  });

  it("asks with a Connect GET, the request's JSON in the query", async () => {
    const fetch = answer(Response.json({ session: { sessionId: "a" } }));
    expect(await client.getSession({ sessionId: "a b" }, signal())).toEqual({ session: { sessionId: "a" } });
    const request = fetch.mock.calls[0]![0] as Request;
    const url = new URL(request.url);
    expect(request.method).toBe("GET");
    expect(url.pathname).toBe("/twr.pitwall.v1.PitwallService/GetSession");
    expect(Object.fromEntries(url.searchParams)).toEqual({ connect: "v1", encoding: "json", message: '{"sessionId":"a b"}' });
  });

  it.each([
    ["not_found", 404, 404],
    ["invalid_argument", 400, 400],
    ["unavailable", 503, 503],
    ["internal", 500, 500],
  ])("Connect's %s is an ApiError %d", async (code, http, status) => {
    answer(Response.json({ code, message: "no such session" }, { status: http }));
    const err = await client.getSession({ sessionId: "x" }, signal()).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(status);
    expect((err as ApiError).message).toBe("no such session");
  });

  it("an answer that is not Connect's error keeps its HTTP status", async () => {
    answer(new Response("<html>", { status: 502, statusText: "Bad Gateway" }));
    const err = (await client.listLaps({ sessionId: "x" }, signal()).catch((e: unknown) => e)) as ApiError;
    expect([err.status, err.message]).toEqual([502, "Bad Gateway"]);
  });

  it("reads /healthz, 503 and all, and fails on a body that is not JSON", async () => {
    answer(Response.json({ ok: false, version: "1.0.0", nats: true, logbook: false }, { status: 503 }));
    expect((await health(signal())).version).toBe("1.0.0");
    answer(new Response("down", { status: 502 }));
    await expect(health(signal())).rejects.toBeInstanceOf(ApiError);
  });
});

describe("the tests' fake API", () => {
  const call = (method: string, message: unknown) =>
    new Request(
      `http://pitwall/twr.pitwall.v1.PitwallService/${method}?connect=v1&encoding=json&message=${encodeURIComponent(JSON.stringify(message))}`,
    );

  it("names each call by v1's route", () => {
    expect(v1Url(call("ListSessions", { track: "Fuji", timeZone: "Europe/Dublin" })).toString()).toBe(
      "http://pitwall/api/v1/sessions?track=Fuji&tz=Europe%2FDublin",
    );
    expect(v1Url(call("GetTrace", { sessionId: "a b", lap: 3, points: 900 })).toString()).toBe(
      "http://pitwall/api/v1/sessions/a%20b/laps/3/trace?points=900",
    );
    expect(v1Url(new Request("http://pitwall/healthz")).pathname).toBe("/healthz");
  });

  it("answers v1's errors as Connect's", async () => {
    const resp = await toConnect(call("GetSession", { sessionId: "x" }), Response.json({ error: "no such session" }, { status: 404 }));
    expect(resp.status).toBe(404);
    expect(await resp.json()).toEqual({ code: "not_found", message: "no such session" });
    const down = await toConnect(call("GetEngineStatus", {}), new Response("bad gateway", { status: 502, statusText: "Bad Gateway" }));
    expect(await down.json()).toEqual({ code: "unavailable", message: "Bad Gateway" });
  });
});
