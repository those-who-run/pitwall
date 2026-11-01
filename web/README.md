# The web app

For anyone working on the React pit wall in `web/`: its stack, the rules the build enforces, Node, and the commands. What a driver sees is in [the pit wall guide](../docs/pitwall.md).

`web/` is the React pit wall (v1's
[ADR 0003](https://github.com/those-who-run/those-who-run/blob/v1.0.0/docs/adr/0003-ui-stack.md),
kept by paddock's [v1 records](https://github.com/those-who-run/paddock/blob/main/docs/adr/README.md#v1s-records)): Vite,
React and TypeScript (strict), Tailwind, Radix through shadcn/ui components in
`web/src/components/ui/`, TanStack Query, and React Router in data mode. It
builds to static files in `web/dist`, which the Go server, `cmd/pitwall`, serves
from `TWR_PITWALL_STATIC_DIR` beside the API on one origin
([Running it](../docs/pitwall.md#running-it)): the session list, the session page
and lap compare ([the pit wall guide](../docs/pitwall.md)); a results card's old `/?session=<id>`
link lands on `/sessions/<id>`.

- **Data** comes only through the Connect client generated from `contracts`
  at the tag `web/buf.gen.yaml` pins into `web/src/api/gen/` (`npm run api` in
  `web/`; those-who-run/paddock#127), and the TanStack Query hooks in
  `web/src/api/hooks.ts`. ESLint fails `fetch` or
  the client anywhere outside `web/src/api/`.
- **Style comes only from the design tokens** (v1's ADR 0004,
  [`docs/design.md`](../docs/design.md)) in `web/src/styles/`: ESLint fails a
  colour literal or an arbitrary Tailwind value anywhere else in `web/src/`, a
  Vitest test checks every allowed colour pair's contrast, and the brand
  colours are `web/src/styles/brand.css`, edited by hand. The fonts (Barlow and
  Barlow Semi Condensed) are self-hosted from `@fontsource`; the app is dark
  only.
- **Numbers become text only in `web/src/lib/format.ts`**, whose cases are
  contracts' `format/format-cases.json` at the pinned tag.
- **No browser-only code at module top level**: a Vitest test imports every
  route module under Node with no DOM.
- **Each route is a lazy chunk**, under a gzipped budget in
  `web/bundle-budget.json` (the entry, and each route at most 60 KB). `node
  scripts/bundle-budget.ts --set` in `web/` resets it to the build plus 10%.
- **Playwright** (`make web-e2e`, CI's `e2e` job; those-who-run/paddock#130)
  runs the built app against the real `pitwall` over NATS and the pinned
  `logbook` image, which serves `web/e2e/store/data`: v1's recordings replayed
  through `ecu` by `web/e2e/store/make.go`, so no `ecu` runs in CI. It runs in
  Chromium at 390×844, 820×1180 and 1440×900, one test at a time, checks nothing
  scrolls sideways and saves screenshots to `web/test-results/screenshots/`,
  which CI uploads. The helpers in `web/e2e/` (the set-up, the API, the
  viewports, the screenshot) are shared by every page's spec. The engineer is
  the live KV bucket (`web/e2e/engineer.ts`): down unless a test asks for a
  state with `setEngineer(page, "driving")`.

Node (26 LTS) is pinned in `web/.node-version` and `engines`, every package
exactly in `web/package-lock.json`, installed with `npm ci`. Add or change a
package with the npm that ships with that Node.

```sh
make web-build       # web/dist
make web-dev         # Vite on http://localhost:5173, the API and /healthz proxied to PITWALL_URL
make web-check       # ESLint, tsc, client freshness, Vitest, the build, bundle budget
```

`make web-dev` proxies `/twr.pitwall.v1.PitwallService` and `/healthz` to
`PITWALL_URL`, `http://127.0.0.1:9852` by default: the pit wall paddock's
compose stack runs (`make pitwall` in paddock). For one run from this checkout,
set it to that one's address, such as
`PITWALL_URL=http://127.0.0.1:8081 make web-dev`.

**On the phone and iPad:** v1's `make web-preview` has no v2 counterpart;
[the pit wall guide](../docs/pitwall.md#on-the-phone-and-ipad) says what to do
instead.
