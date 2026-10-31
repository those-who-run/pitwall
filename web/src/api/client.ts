// The one place the app talks HTTP (ADR 0003 point 3): the client for
// twr.pitwall.v1.PitwallService, typed by the code generated from contracts
// (./gen, those-who-run/paddock#127). Components use the hooks in ./hooks.ts,
// never this; ESLint stops `fetch` and this module being used outside src/api/.
//
// It speaks Connect's protocol for a unary method with no side effects, which
// is plain HTTP: a GET of /<service>/<method> with the request's protobuf JSON
// in the query, answered with the response's JSON, or an error's JSON with its
// code. Only types are imported from the generated code and protobuf-es, so no
// protobuf runtime is bundled: connect-web and protobuf-es's runtime would add
// about 34 KB gzipped to every data page, past ADR 0003's 60 KB a route (the
// maintainer's decision, 2026-10-31: keep this client).
import type { MessageJsonType } from "@bufbuild/protobuf";
import type { GenService } from "@bufbuild/protobuf/codegenv2";

import type { Health } from "./adapt";
import type { PitwallService } from "./gen/twr/pitwall/v1/pitwall_pb";

/** The service's methods as the generated code names them (listSessions, …),
 * with their messages: a method renamed or retyped in contracts fails `tsc`. */
type Methods = typeof PitwallService extends GenService<infer M> ? M : never;
type Method = keyof Methods & string;
/** A method's request and response in protobuf's JSON form. */
export type Input<M extends Method> = MessageJsonType<Methods[M]["input"]>;
export type Output<M extends Method> = MessageJsonType<Methods[M]["output"]>;

/** The service's path: its full name, as Connect routes it. */
export const SERVICE_PATH = "/twr.pitwall.v1.PitwallService/";

/** A failed call, with the HTTP status it means (v1's ApiError): 404 for a
 * session or lap that isn't there, 400 a bad argument, 503 the pit wall's store
 * not answering. */
export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

/** Connect's error codes as the HTTP statuses v1's API answered. */
const statuses: Record<string, number> = {
  invalid_argument: 400,
  not_found: 404,
  unimplemented: 404,
  unavailable: 503,
  deadline_exceeded: 504,
};

/** A method's RPC name, as Connect routes it: the generated name with its first
 * letter upper case again (listSessions is ListSessions). */
export function rpcName(method: string): string {
  return method.charAt(0).toUpperCase() + method.slice(1);
}

/** `path` on the page's own origin. */
function here(path: string): URL {
  return new URL(path, window.location.href);
}

/** One call: a GET, so the browser's cache keeps the answer and sends its ETag
 * back as If-None-Match, and a poll that changes nothing is a 304 the browser
 * answers from its cache. Same origin: the pit wall serves the app and the API
 * (ADR 0003 point 6). fetch is looked up per call, so a test can replace it. */
async function call<M extends Method>(method: M, input: Input<M>, signal: AbortSignal): Promise<Output<M>> {
  const query = new URLSearchParams({ connect: "v1", encoding: "json", message: JSON.stringify(input) });
  const response = await fetch(new Request(here(`${SERVICE_PATH}${rpcName(method)}?${query}`), { signal }));
  if (response.ok) {
    return (await response.json()) as Output<M>;
  }
  let error: { code?: string; message?: string } = {};
  try {
    error = (await response.json()) as typeof error;
  } catch {
    // not Connect's error JSON: a proxy's page, say
  }
  const status = (error.code && statuses[error.code]) || (response.status >= 400 ? response.status : 500);
  throw new ApiError(status, error.message || response.statusText || `HTTP ${response.status}`);
}

/** PitwallService's methods, each typed by the generated service: a method
 * contracts drops or renames is a compile error here. */
export const client = {
  listSessions: (input: Input<"listSessions">, signal: AbortSignal) => call("listSessions", input, signal),
  getSession: (input: Input<"getSession">, signal: AbortSignal) => call("getSession", input, signal),
  listLaps: (input: Input<"listLaps">, signal: AbortSignal) => call("listLaps", input, signal),
  getTrace: (input: Input<"getTrace">, signal: AbortSignal) => call("getTrace", input, signal),
  listSameTrack: (input: Input<"listSameTrack">, signal: AbortSignal) => call("listSameTrack", input, signal),
  getEngineStatus: (input: Input<"getEngineStatus">, signal: AbortSignal) => call("getEngineStatus", input, signal),
};

/** The pit wall's GET /healthz, which is plain HTTP. Unhealthy (a 503) has the
 * same body, version and all. */
export async function health(signal: AbortSignal): Promise<Health> {
  const response = await fetch(new Request(here("/healthz"), { signal }));
  try {
    return (await response.json()) as Health;
  } catch {
    throw new ApiError(response.status, response.statusText || `HTTP ${response.status}`);
  }
}
