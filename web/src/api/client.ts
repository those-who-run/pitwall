// The one place the app talks HTTP (ADR 0003 point 3): the client for
// twr.pitwall.v1.PitwallService, typed by the code generated from contracts
// (./gen, those-who-run/paddock#127). Components use the hooks in ./hooks.ts,
// never this; ESLint stops `fetch` and this module being used outside src/api/.
//
// It speaks Connect's protocol for a unary method with no side effects, which
// is plain HTTP: a GET of /<service>/<method> with the request's protobuf JSON
// in the query, answered with the response's JSON, or an error's JSON with its
// code. Only the generated JSON types are imported, so no protobuf runtime is
// bundled: connect-web and protobuf-es's runtime would add about 34 KB gzipped
// to every data page, past ADR 0003's 60 KB a route.
import type { Health } from "./adapt";
import type {
  GetEngineStatusRequestJson,
  GetEngineStatusResponseJson,
  GetSessionRequestJson,
  GetSessionResponseJson,
  GetTraceRequestJson,
  GetTraceResponseJson,
  ListLapsRequestJson,
  ListLapsResponseJson,
  ListSameTrackRequestJson,
  ListSameTrackResponseJson,
  ListSessionsRequestJson,
  ListSessionsResponseJson,
} from "./gen/twr/pitwall/v1/pitwall_pb";

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

/** `path` on the page's own origin. */
function here(path: string): URL {
  return new URL(path, window.location.href);
}

/** One call: a GET, so the browser's cache keeps the answer and sends its ETag
 * back as If-None-Match, and a poll that changes nothing is a 304 the browser
 * answers from its cache. Same origin: the pit wall serves the app and the API
 * (ADR 0003 point 6). fetch is looked up per call, so a test can replace it. */
async function call<Req, Res>(method: string, input: Req, signal: AbortSignal): Promise<Res> {
  const query = new URLSearchParams({ connect: "v1", encoding: "json", message: JSON.stringify(input) });
  const response = await fetch(new Request(here(`${SERVICE_PATH}${method}?${query}`), { signal }));
  if (response.ok) {
    return (await response.json()) as Res;
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

/** PitwallService's methods, typed by the generated JSON types. */
export const client = {
  listSessions: (input: ListSessionsRequestJson, signal: AbortSignal) =>
    call<ListSessionsRequestJson, ListSessionsResponseJson>("ListSessions", input, signal),
  getSession: (input: GetSessionRequestJson, signal: AbortSignal) =>
    call<GetSessionRequestJson, GetSessionResponseJson>("GetSession", input, signal),
  listLaps: (input: ListLapsRequestJson, signal: AbortSignal) =>
    call<ListLapsRequestJson, ListLapsResponseJson>("ListLaps", input, signal),
  getTrace: (input: GetTraceRequestJson, signal: AbortSignal) =>
    call<GetTraceRequestJson, GetTraceResponseJson>("GetTrace", input, signal),
  listSameTrack: (input: ListSameTrackRequestJson, signal: AbortSignal) =>
    call<ListSameTrackRequestJson, ListSameTrackResponseJson>("ListSameTrack", input, signal),
  getEngineStatus: (input: GetEngineStatusRequestJson, signal: AbortSignal) =>
    call<GetEngineStatusRequestJson, GetEngineStatusResponseJson>("GetEngineStatus", input, signal),
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
