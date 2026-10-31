# The pit wall

For the driver and anyone running the pit wall: the web app as a driver uses it, the data API behind it, the store it reads, the preview on a phone or iPad, and the pitwall image. Back to v1's [docs index](https://github.com/those-who-run/those-who-run/blob/v1.0.0/docs/README.md).

This page came over from v1 with the web app (those-who-run/paddock#120). "Running it", "The data API" and "The pitwall image" describe v2's server (those-who-run/paddock#124, those-who-run/paddock#131); the rest still describes v1 until those-who-run/paddock#127 brings v2's.

## Pages

The pit wall is a React app over the session store, served with its read-only
data API by `those-who-run pitwall` on port 9852 (`--port` or
`GT7_PITWALL_PORT`, `--address` to bind; ADR 0003). One column, so it works on
a phone straight after a race:

- **Sessions** (`/`): grouped by day in your browser's time zone, newest first,
  each with its results card (title, result, best lap, car, fuel used). The
  session being driven is pinned at the top with the engineer's lap and
  position, and follows its laps without a reload. Filter by track, car,
  session kind and dates; the filters live in the URL. A session with no full
  lap is tucked away until asked for (`?all=1`).
- **A session** (`/sessions/<id>`): the result first, then stat tiles, the race
  story (lap time and position by lap) and the laps: time, gap to the best lap,
  fuel used and left, position (only in a field GT7 flags, as on the card), top
  speed and flags: **pit** and **out** for the in-lap and out-lap the pit-stop
  detector stored (tyre-only stops included; sessions stored before it fall back
  to a rise in fuel), **partial** for the lap the session ended in. GT7 does not
  report track limits. Pick two laps to compare; the picks are in the URL.
- **Lap compare** (`/sessions/<id>/compare?a=&b=`): speed, throttle, brake, gear
  and RPM of two laps over time from each lap's start, from the same session or
  another at the same track, with one cursor across every chart (a horizontal
  drag scrubs on a phone), drag or pinch to zoom. Comparing by distance instead of
  time is planned.

| | Phone | Desktop |
|---|---|---|
| Sessions | ![The session list on a phone: today's sessions as cards, each with its track, kind, car, time, result and best lap](assets/screens/phone/sessions.png) | ![The session list on a desktop: filters for track, car, kind and date over today's four sessions](assets/screens/desktop/sessions.png) |
| A session | ![A 12-lap race at Circuit Gilles Villeneuve on a phone: P15 of 20, retired on lap 3, and stat tiles for best lap, average, laps, fuel used, pit stops and driving time, then the laps](assets/screens/phone/session.png) | ![The same race on a desktop: the result, the stat tiles and the lap table, with lap 1 the best and lap 3 partial](assets/screens/desktop/session.png) |
| Lap compare | ![Lap compare on a phone: lap 1 against lap 2 of the race, the cursor readout, and the speed and throttle charts](assets/screens/phone/compare.png) | ![Lap compare on a desktop: lap 1, the best, against lap 2, with speed and throttle charts of both laps](assets/screens/desktop/compare.png) |
| `/_kit` | ![The component kit on a phone: lap times, gaps and positions in each of their states](assets/screens/phone/kit.png) | ![The component kit on a desktop: lap times, gaps, positions and kind badges in each of their states, loading included](assets/screens/desktop/kit.png) |

`/_kit` is the component kit's review page (ADR 0004), not a page a driver
uses.

A results card's old link, `/?session=<id>`, opens that session. The pit wall
only reads: the index is opened read-only and no session data is written.

## The shell

Every page sits in the shell (`web/src/app/`): a header with the 走り屋
wordmark and the **live engineer chip**, which asks `GetEngineStatus` every
5 s while the tab is visible and says "Live · Suzuka Circuit · L3", "Paused",
"Replay", "Demo", "Idle" (or "In the menus"), or "Engineer offline" when the
engineer doesn't answer; the pages carry on either way. Tap it for the
engineer's version, telemetry and voice ("Unknown" when `radio` says nothing
of it), a link to the live session, and the pit wall's own version. The two
version apart in v2, so a difference is not a warning
(those-who-run/paddock#125 R7). The sections' navigation (`web/src/app/sections.ts`) appears once
there are two. **Add to Home Screen** on the iPhone (Safari, Share, Add to Home
Screen) gives the 走り屋 icon and opens the pit wall full screen with no Safari
bars, from `web/public/manifest.webmanifest`; there is no service worker, so a
deploy is never hidden behind a stale cache.

## Running it

`pitwall` (`cmd/pitwall`) is v2's pit wall server, in Go (those-who-run/paddock#124).
It holds no data and mounts no volume: the sessions come from `logbook`, over
NATS, and the engineer's status from the `live` KV bucket. One origin serves
the API, `/healthz` and the built web app (`make web-build`, which needs Node;
see [the web app's guide](../web/README.md)):

```sh
make web-build
TWR_NATS_URL=nats://127.0.0.1:4222 TWR_PITWALL_STATIC_DIR=web/dist go run ./cmd/pitwall   # http://localhost:8080
```

Its configuration, from the environment (the `TWR_*` prefix is the
maintainer's decision D5, 2026-10-31; `ADDR` and `LOG_LEVEL` are every
component's):

| Variable | Default | Purpose |
|---|---|---|
| `ADDR` | `:8080` | Where it listens; compose publishes it on 9852, as v1's port. |
| `LOG_LEVEL` | `info` | `debug` adds an access log line per request: method, path, status and time, never a header or the query. |
| `TWR_NATS_URL` | `nats://127.0.0.1:4222` | The NATS server `logbook` and the `live` bucket are on. NATS down at the start doesn't stop the pit wall: it keeps trying, and serves the web app meanwhile. |
| `TWR_PITWALL_STATIC_DIR` | unset; `/web` in the image | The built web app. Unset serves the API and `/healthz` only. |
| `TWR_PITWALL_SRC` | `live,v1-import` | The sources whose sessions it shows, comma separated, in order of preference: v2's own and v1's imported history (decision D7). A list naming no source is an error, never "every source". |
| `TWR_PITWALL_STALE` | `15s` | How old `ecu`'s live status may be before the chip says "Engineer offline". |

It has no login: keep it on the LAN (decision D1). It stops within 1 s of
SIGTERM, so a rollout never waits on it.

## The preview on the phone and iPad

For a pit wall that runs in Kubernetes. **On the phone and iPad from a
laptop**: `make web-preview PREVIEW_CONTEXT=<kube context>` builds `web/dist`, copies a deployed store into
`.preview/` (git-ignored) by `tar` out of the pit wall pod's `/data` with
`kubectl exec` (it only reads there, never writes), and runs `those-who-run
pitwall --address 0.0.0.0 --static web/dist` over the copy. Open
`http://<laptop>:9852` on the same network. `PREVIEW_CONTEXT` names the kube
context the pit wall runs in and has no default. `/api/engine/*` goes to the live
engineer, on port 9850 of the node its pod runs on (asked of the cluster), so
the engineer's status is live while the sessions are the copy;
`PREVIEW_ENGINEER` overrides it.

## The data API

The API is `twr.pitwall.v1.PitwallService`, a Connect service defined in
`contracts` (contracts#41), at `/twr.pitwall.v1.PitwallService/<Method>`. Every
method has no side effects, so the web app calls it with GET and the browser's
cache works.

| Method | What it answers | v1's route |
|---|---|---|
| `ListSessions` | Sessions, newest first, filtered by track, car, kind and days in the viewer's zone, with the values each filter can take | `GET /api/v1/sessions` |
| `GetSession` | One session | `GET /api/v1/sessions/{id}` |
| `ListLaps` | Its lap table, flags included (`pit`, `out_lap`, `invalid`, `restart`, `best`, `clean`) | `GET /api/v1/sessions/{id}/laps` |
| `GetTrace` | One lap's speed, throttle, brake, gear, RPM and position against time | `GET /api/v1/sessions/{id}/laps/{lap}/trace` |
| `ListSameTrack` | The other sessions at the same track, for lap compare | `GET /api/v1/sessions/{id}/same-track` |
| `GetEngineStatus` | What the engineer is doing now, for the live chip | `GET /api/engine/status` |
| `GET /healthz` (plain HTTP) | `ok`, `version`, and whether `nats` and `logbook` answered | `GET /healthz` |

- **From `logbook`.** Each data method asks `logbook`'s micro endpoints
  (`svc.logbook.*`, `twr.store.v1`) for the sources in `TWR_PITWALL_SRC`, and
  maps the answer (decision D6): `logbook` keeps `ecu`'s summary and laps as
  `ecu` published them, and the pit wall works out the names, flags and labels
  the page shows (`internal/api/map.go`): the track's name as the card names it
  (the layout when the detector is sure, else the circuit, else "Unknown
  track"), which the track filter takes too, the lap flags from `ecu`'s pit roles, `ecu`'s best lap, the
  restart laps from the session's gaps, the position only in a field GT7 flags.
- **The card's words.** A session's `title`, `result` and `kind_label` are the
  results card's, from `contracts`' `format` package (`Title`, `Result`,
  `KindLabel`), which `podium`'s card calls too, so the page and the card never
  name a session differently. The track's name and `position_shown` come from
  the same package.
- **ETags.** Every data answer carries a weak `ETag` holding `logbook`'s
  `data_version` and `Cache-Control: no-cache`. A GET with that ETag in
  `If-None-Match` passes the version to `logbook` as `known_data_version`, which
  answers `unchanged` at next to no cost, and the pit wall answers 304 with no
  body. Answers over 1 KB are gzipped when the browser takes it.
- **Errors** are Connect's: a session or lap that isn't there is `not_found`, a
  bad argument `invalid_argument`, and `logbook` not answering within 2 s
  `unavailable`. The web app is served all the same.
- **The engineer's status** comes from a watch on `ecu.status.live`,
  `ecu.session.live` and `radio.status.live` in the `live` bucket, kept open for
  the server's life (those-who-run/paddock#125). Only the `live` source's keys
  are read, so a replay never shows. With no value, a value older than
  `TWR_PITWALL_STALE` or NATS down it answers `ACTIVITY_OFFLINE`, never an
  error, and it is never cached (`no-store`). `voice_connected` is absent when
  `radio` says nothing of it.
- **`/healthz`** is 200 whether or not NATS and `logbook` answer, and 503 only
  when `TWR_PITWALL_STATIC_DIR` is set and its `index.html` can't be read.
- **The web app** is served from `TWR_PITWALL_STATIC_DIR` on every other path,
  with `index.html` for client routes such as `/sessions/<id>`, `/_kit` and
  v1's results-card link `/?session=<id>` (permanent, ADR 0009 and 0011).
  Hashed files under `assets/` are cached for a year, and a missing one is a
  404; `index.html` is never cached.

## The store

Every frame of every lap is kept, so the pit wall can show
racing lines, traces and growth over time. Under `GT7_DATA_DIR`:

```
index.sqlite                      sessions and laps tables, for listing and filtering
index.sqlite-wal, -shm            SQLite's write-ahead log, while a process has it open
engineer.lock                     the writer lock: held by whatever is writing the store
sessions/<id>/lap-NN.parquet      60 Hz frames for that lap, ZSTD compressed
sessions/<id>/summary.json        human-readable summary
```

Frames are buffered in memory and written on a background thread when the lap
completes, so a crash loses at most the lap in progress and callouts are never
held up by disk. A lap's row goes into the index only once its file is
complete, so the pit wall never shows a lap without its frames. A partial lap at the end of a session is written with
`complete = 0`. Restarting from the pause menu closes the session and opens a
new one; quitting closes it; Ctrl-C closes whatever is open. Budget roughly
20 MB per hour of driving (17.5 to 23 MB measured). Parquet is written and
read with DuckDB; any Parquet-aware tool can read the files. From 0.3 frames also carry the body
rotation (`rot_x`, `rot_y`, `rot_z`, `orientation`) and spin (`ang_vel_*`),
raw from the packet, for drift detection; older laps lack those columns.

The index is in SQLite's WAL mode, so the pit wall reading it never holds up
the engineer's writes. There is one writer at a time (ADR 0001): `run` holds
an exclusive lock on `engineer.lock` for as long as it is up, and every other
command that writes the store (`tracks backfill`, `post`, `rebuild-best`,
`replay --store` into it) refuses with exit code 1 while it does. Use the pit
wall, or stop the engineer first. The engineer likewise refuses to start while
one of those is running.
The pit wall only reads and never takes the lock.

## The pitwall image

`ghcr.io/those-who-run/twr-pitwall:X.Y.Z` is the pit wall's image
(those-who-run/paddock#131; images on ghcr, paddock ADR 0006). The
`Dockerfile` builds the web app in Node's image (the version in
`web/.node-version`, with `npm ci` and `npm run build`), the server in Go's
(`CGO_ENABLED=0`, its version built in with `-ldflags` from the release tag),
and runs both from distroless's static image as its non-root user (65532):
the binary at `/service` and `web/dist` at `/web`, with
`TWR_PITWALL_STATIC_DIR=/web` set, on port 8080. It has no Node, no Python
and no shell. A deployment probes `GET /healthz`, which is 503 when `/web`'s
`index.html` can't be read.

Every PR builds it and runs `scripts/image_smoke.sh` on it, pushing nothing:
the version, the non-root user, under 40 MB, no Node or Python, `/healthz`
answering 200 with the version within 10 s, `index.html` on `/`, on a
session's page and on v1's card link `/?session=<id>`, and `GetEngineStatus`
answering offline over Connect's GET. A
release tag `vX.Y.Z` builds it again, runs the same smoke test expecting
`X.Y.Z`, and `image-publish.yml` in `those-who-run/.github` pushes that build
with its SBOM and provenance, attested. Check an image with:

```sh
gh attestation verify oci://ghcr.io/those-who-run/twr-pitwall:X.Y.Z -R those-who-run/pitwall \
  --signer-workflow those-who-run/.github/.github/workflows/image-publish.yml
```
