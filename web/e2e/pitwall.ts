// What every pit wall spec shares (#237 R11): `test` pointed at the pit wall the
// global setup started, the API as the specs read it, the layout checks and the
// screenshot for the CI artifact. A new page's spec imports `test` and `expect`
// from here and adds its own steps.
//
// v2 (those-who-run/paddock#130): the API is twr.pitwall.v1.PitwallService over
// Connect's GET, and the helpers below hand the specs its answers in the shapes
// the pages read (src/api/adapt.ts, v1's), so the specs' expectations stay v1's.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

import { test as base, expect, type Locator, type Page, type Request, type TestInfo } from "@playwright/test";

import { lapList, type LapRow, type Session, session, sessionList, trace, type Trace } from "../src/api/adapt";
import { setEngineer } from "./engineer";

/** Where screenshots go, one directory per viewport: CI uploads it. */
export const SCREENSHOTS = fileURLToPath(new URL("../test-results/screenshots", import.meta.url));

/** The browser's clock in every test: later on the day the store's sessions were
 * replayed (web/e2e/store/make.go), so they are "Today", as v1's stores, replayed
 * on the day of the run, were. The time zone is UTC (playwright.config.ts). */
export const NOW = new Date("2026-10-15T14:00:00Z");

export const test = base.extend<{ resetEngineer: void }>({
  baseURL: async ({}, use) => {
    const url = process.env.PITWALL_URL;
    if (!url) {
      throw new Error("PITWALL_URL is not set: run through playwright.config.ts's global setup");
    }
    await use(url);
  },
  page: async ({ page }, use) => {
    await page.clock.setSystemTime(NOW);
    await use(page);
  },
  // Each test starts with the engineer down, as v1's stub was until a test asked
  // for a scenario: the live bucket is shared (./engineer.ts).
  resetEngineer: [
    async ({}, use) => {
      await setEngineer(null, "down");
      await use();
    },
    { auto: true },
  ],
});

export { expect };

/** The address of the pit wall over one of the global setup's other stores
 * (#297): "short", "empty", "broken" or "compare". */
export function storeUrl(name: "short" | "empty" | "broken" | "compare"): string {
  const url = (JSON.parse(process.env.PITWALL_STORES ?? "{}") as Record<string, string>)[name];
  if (!url) {
    throw new Error(`no pit wall for the ${name} store: run through playwright.config.ts's global setup`);
  }
  return url;
}

export const SERVICE = "/twr.pitwall.v1.PitwallService/";

/** A Connect GET of `method` with `input`, as src/api/client.ts makes it. */
export function connectPath(method: string, input: Record<string, unknown> = {}): string {
  return `${SERVICE}${method}?connect=v1&encoding=json&message=${encodeURIComponent(JSON.stringify(input))}`;
}

/** The method a request calls and its input, or null for any other request. */
export function connectCall(url: string): { method: string; input: Record<string, unknown> } | null {
  const u = new URL(url);
  if (!u.pathname.startsWith(SERVICE)) {
    return null;
  }
  const message = u.searchParams.get("message");
  return { method: u.pathname.slice(SERVICE.length), input: message ? JSON.parse(message) : {} };
}

/** A matcher for page.route: calls of `method`, for `sessionId` when given. v1's
 * specs routed `/api/v1/sessions/<id>/laps` and the like. */
export function calls(method: string, sessionId?: string): (url: URL) => boolean {
  return (url) => {
    const call = connectCall(url.href);
    return call?.method === method && (sessionId === undefined || call.input.sessionId === sessionId);
  };
}

/** Whether a request the page made calls `method`. */
export function isCall(request: Request, method: string): boolean {
  return connectCall(request.url())?.method === method;
}

/** The answer to a Connect GET, its body's JSON. */
async function api(page: Page, base: string, method: string, input: Record<string, unknown>) {
  const resp = await page.request.get(`${base}${connectPath(method, input)}`);
  expect(resp.ok(), `${method} ${JSON.stringify(input)}: ${resp.status()}`).toBe(true);
  return resp.json();
}

/** Every API session, as the page should list them; `base` is another store's
 * pit wall, or the main one. */
export async function apiSessions(page: Page, base = ""): Promise<Session[]> {
  return sessionList(await api(page, base, "ListSessions", { timeZone: "UTC" })).sessions;
}

/** One session, as the session page reads it. */
export async function apiSession(page: Page, id: string, base = ""): Promise<Session> {
  return session((await api(page, base, "GetSession", { sessionId: id })).session);
}

/** A session's laps, as the session page reads them. */
export async function apiLaps(page: Page, id: string, base = ""): Promise<LapRow[]> {
  return lapList(await api(page, base, "ListLaps", { sessionId: id })).laps;
}

/** A lap's trace thinned to `points`, as lap compare reads it. */
export async function apiTrace(page: Page, id: string, lap: number, points: number, base = ""): Promise<Trace> {
  return trace((await api(page, base, "GetTrace", { sessionId: id, lap, points })).trace, id, lap);
}

/** A session's card link in the list (#297): the whole card is the link, named by
 * its track, so a spec finds it by where it goes. */
export function sessionLink(page: Page, id: string): Locator {
  return page.getByRole("main").locator(`a[href="/sessions/${id}"]`);
}

/** The page is no wider than the viewport: nothing scrolls sideways. */
export async function expectNoHorizontalScroll(page: Page): Promise<void> {
  const { scroll, client } = await page.evaluate(() => ({
    scroll: document.documentElement.scrollWidth,
    client: document.documentElement.clientWidth,
  }));
  expect(scroll, `the page is ${scroll}px wide in a ${client}px viewport`).toBeLessThanOrEqual(client);
}

/** A full-page screenshot, `<viewport>/<name>.png` under SCREENSHOTS. */
export async function screenshot(page: Page, testInfo: TestInfo, name: string): Promise<string> {
  const dir = join(SCREENSHOTS, testInfo.project.name);
  mkdirSync(dir, { recursive: true });
  const path = join(dir, `${name}.png`);
  await page.screenshot({ path, fullPage: true });
  await testInfo.attach(name, { path, contentType: "image/png" });
  return path;
}
