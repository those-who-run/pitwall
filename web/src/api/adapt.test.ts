// The adapter (those-who-run/paddock#127 R3): a generated message, as the pit
// wall sends it, comes out as the shape the pages read, and the tests' fake API
// (./testing.ts) is its exact inverse, so the pages' tests answer what the pit
// wall would.
import { afterEach, describe, expect, it, vi } from "vitest";

import { engine, liveSession, LAPS, SESSIONS } from "@/kit/samples";

import * as adapt from "./adapt";
import { ApiError, client, health } from "./client";
import type { EngineStatusJson, LapRowJson, SessionJson, TraceJson } from "./gen/twr/pitwall/v1/pitwall_pb";
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

describe("the client (R1, R6)", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  const answer = (response: Response) => vi.spyOn(globalThis, "fetch").mockResolvedValue(response);
  const signal = () => new AbortController().signal;

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
