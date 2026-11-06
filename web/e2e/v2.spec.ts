// What v2's pit wall adds to v1's specs, over the same set-up (those-who-run/paddock#130,
// with the checks moved to it from #125 and #127): the browser's cache and the pit
// wall's ETags, and ecu's own state at the end of a replay.
import { expectAccessible } from "./a11y";
import { writeLive } from "./engineer";
import { apiSession, apiSessions, connectCall, connectPath, expect, storeUrl, test } from "./pitwall";
import { MANIFEST, SESSION } from "./store";

const PIT_STOP = "Suzuka Circuit East Course · 6-lap race";

test("a second visit to a session page within 5 s gets 304s for its data calls (paddock#127 AC5)", async ({
  page,
}) => {
  const s = (await apiSessions(page)).find((x) => x.title === PIT_STOP && x.closed);
  expect(s, `a closed session titled ${PIT_STOP}`).toBeDefined();
  await page.goto(`/sessions/${s!.session_id}`);
  await expect(page.locator("tbody tr[data-lap]")).toHaveCount(6);

  // The network log of the second visit: each data call's status as it came over
  // the wire. Chromium hands the page a revalidated answer as the cached 200, and
  // Playwright's response events report that; the DevTools protocol's extra info
  // has the pit wall's own status line, the 304 DevTools shows.
  //
  // The two events come down different pipes (requestWillBeSent from the page's
  // renderer, responseReceivedExtraInfo from the network service), and Chromium
  // does not order them. The most likely cause of this test's flakes was a 304
  // from localhost reporting its status before the page had said what it asked
  // for, which the test dropped. Each is now kept by request id and the two are
  // joined when the test reads them, in whatever order they came. A call whose
  // extra info never comes fails as an empty list, [], not undefined.
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("Network.enable");
  const methods = new Map<string, string>();
  const codes = new Map<string, number[]>();
  cdp.on("Network.requestWillBeSent", (e) => {
    const call = connectCall(e.request.url);
    if (call && call.method !== "GetEngineStatus") {
      methods.set(e.requestId, call.method);
    }
  });
  cdp.on("Network.responseReceivedExtraInfo", (e) => {
    codes.set(e.requestId, [...(codes.get(e.requestId) ?? []), e.statusCode]);
  });
  const statuses = (method: string) =>
    [...methods].filter(([, m]) => m === method).flatMap(([id]) => codes.get(id) ?? []);
  const start = Date.now();
  await page.goto(`/sessions/${s!.session_id}`);
  await expect(page.locator("tbody tr[data-lap]")).toHaveCount(6);
  expect(Date.now() - start).toBeLessThan(5_000);
  // The browser sends the pit wall's ETag back, and nothing has changed.
  await expect.poll(() => statuses("GetSession")).toEqual([304]);
  await expect.poll(() => statuses("ListLaps")).toEqual([304]);
});

test("after a replay through ecu: the engineer is idle and the session is closed (paddock#125 AC5)", async ({
  page,
}, testInfo) => {
  // What ecu left in the live bucket under the replay's own source once its last
  // replay ended (web/e2e/store/make.go): idle, and its session key deleted, as v1's
  // status route answered at the end of the same replay. The pit wall reads only
  // the live source's keys, so they are written there.
  const end = MANIFEST.ecu_at_the_end;
  expect(end.status.activity).toBe("ACTIVITY_IDLE");
  expect(end.session).toBeNull();
  await writeLive({
    ecu: { ...end.status, source: "live", updatedAt: new Date().toISOString() },
    session: null,
    radio: null,
  });

  const base = storeUrl("compare");
  const id = SESSION(end.src);
  const session = await apiSession(page, id, base);
  expect(session.closed).toBe(true);
  expect(session.ended_at).not.toBeNull();
  // The status route's answer: idle, with no session.
  const status = await page.request.get(`${base}${connectPath("GetEngineStatus")}`);
  expect(await status.json()).toMatchObject({ status: { activity: "ACTIVITY_IDLE" } });
  expect((await status.json()).status.session).toBeUndefined();
  await page.goto(`${base}/sessions/${id}`);
  // Idle, with the replay's telemetry still connected as ecu left it: the chip's
  // name for that is "In the menus".
  await expect(page.getByRole("banner").locator("[data-slot=live-chip]")).toHaveText("In the menus");
  await expect(page.locator("[data-slot=session-hero] [data-slot=live-badge]")).toHaveCount(0);
  await expect(page.locator("tr[data-in-progress]")).toHaveCount(0);
  await expectAccessible(page, testInfo, "session-after-replay");
});
