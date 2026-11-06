# The repository's checks. `make check` is the Definition of done (AGENTS.md): it
# runs what CI runs, the commit hooks on every file (CI's `checks / checks`), the Go
# checks in go.yml's order (CI's `go / check`), `make test` (CI's `test`, which
# calls this same target so the two can't drift) and `make web-check` (CI's `web`).
# Needs Go (the version in go.mod's toolchain line), python3 and pre-commit, and Node
# (web/.node-version) for the web app; the guard copy check, the first run of the Go
# tools and the first npm ci need the network. `make prereqs` checks for the Go and
# Python tools, and `make check` and `make hooks` run it first.

PY ?= python3
# golangci-lint and govulncheck, at the versions tools/go.mod pins and tools/go.sum
# checksums, as go.yml runs them: by package path, outside any go.work.
GO_TOOL := GOWORK=off go tool -modfile=tools/go.mod
GOLANGCI_LINT := github.com/golangci/golangci-lint/v2/cmd/golangci-lint
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck
# The lowest total statement coverage the tests may reach, in per cent: the
# coverage-floor ci.yml passes to go.yml (tests/test_go_floor.py keeps them equal).
COVERAGE_FLOOR := 90
# The module's Go files, skipping what the go command skips: hidden directories,
# testdata and vendor. And web/, the web app, whose node_modules holds no Go of ours.
GO_FILES = $(shell find . -path ./web -prune -o -name '*.go' -not -path '*/.*' -not -path '*/testdata/*' -not -path '*/vendor/*' -print)
BUILD := build
# The module's packages, without any npm put in web/node_modules (`make web-deps`):
# one of them (flatted) ships a Go package, which would be vetted, tested at 0%
# against the coverage floor and scanned as if it were ours. Evaluated when used.
GO_PKGS = $(shell go list ./... | grep -v /web/node_modules/)

.PHONY: help
help:
	@echo "prereqs   - every tool make check and make hooks need is here, or how to fix it; a new clone or worktree runs it first"
	@echo "check     - everything a PR must pass: the commit hooks on every file, the Go checks, then make test (CI runs all three)"
	@echo "test      - the Go tests (race detector, no skips, coverage floor) and the repository's own checks (CI's test job)"
	@echo "fmt       - format the Go code with gofmt"
	@echo "lint      - golangci-lint, with .golangci.yml"
	@echo "hooks     - install the commit hooks; a new clone or worktree runs it once"
	@echo "fmt-check, tidy, vet, go-test, build, vuln - the Go checks one at a time"
	@echo "links     - every internal link and anchor in the Markdown resolves (offline)"
	@echo "go-scripts - the Go programs run by hand (web/e2e/store/make.go): gofmt and go vet"
	@echo "guard     - .claude/ holds those-who-run/.github's guard and settings at the pinned commit"
	@echo "web-check - the web app: ESLint, tsc, the API client's freshness, Vitest, the build, the bundle budget (CI's web job)"
	@echo "web-build - build the web app into web/dist"
	@echo "web-dev   - Vite's dev server on http://localhost:5173, the API proxied to PITWALL_URL"
	@echo "web-deps  - install web/node_modules from web/package-lock.json (npm ci)"
	@echo "web-e2e   - Playwright over the built app, the Go API, NATS and logbook (needs Go, Docker and Chromium; CI's e2e job)"
	@echo "docs-screenshots - the guide's screenshots into docs/assets/screens/, as web-e2e runs (CI's docs-screenshots workflow)"

# go.yml's order: gofmt, tidy, vet, golangci-lint, the tests, build, govulncheck.
.PHONY: check
check: prereqs
	pre-commit run --all-files
	$(MAKE) fmt-check tidy vet lint go-test build vuln
	$(MAKE) test-repo
	$(MAKE) web-check

.PHONY: test
test: go-test test-repo

# The repository's own checks apart from the Go tests: links, the guard copy, the
# Go programs run by hand, and the tests in tests/.
.PHONY: test-repo
test-repo: links guard go-scripts
	$(PY) -m unittest discover -s tests

# Go at go.mod's toolchain version (or a GOTOOLCHAIN that fetches it), gofmt, cgo's
# C compiler, python3, pre-commit, git and make: one line for each that is missing,
# with the fix. Offline and quick; $(SHELL), not sh from PATH, so it runs whatever
# PATH holds.
.PHONY: prereqs
prereqs:
	@PY=$(PY) $(SHELL) scripts/prereqs.sh

.PHONY: hooks
hooks: prereqs
	pre-commit install

.PHONY: fmt
fmt:
	gofmt -w $(GO_FILES)

# A gofmt that is missing or can't read a file fails, rather than listing nothing.
.PHONY: fmt-check
fmt-check:
	@unformatted=$$(gofmt -l $(GO_FILES)) || exit 1; \
	if [ -n "$$unformatted" ]; then echo "not formatted (make fmt):" $$unformatted >&2; exit 1; fi

# Neither the module nor the tools module may need `go mod tidy`.
.PHONY: tidy
tidy:
	go mod tidy -diff
	cd tools && go mod tidy -diff

.PHONY: vet
vet:
	go vet $(GO_PKGS)

.PHONY: lint
lint:
	$(GO_TOOL) $(GOLANGCI_LINT) run ./...

# The race detector (which needs cgo, whatever the build uses) in shuffled order. A
# skipped test fails, as in go.yml, and so does total coverage below the floor;
# scripts/go_coverage.py totals it, leaving out generated files as go.yml does.
.PHONY: go-test
go-test:
	@mkdir -p $(BUILD)
	CGO_ENABLED=1 go test -race -shuffle=on -covermode=atomic -coverprofile=$(BUILD)/cover.out -v $(GO_PKGS) > $(BUILD)/test.log 2>&1; \
	  status=$$?; grep -v -E '^(=== |\s+--- PASS|--- PASS)' $(BUILD)/test.log; exit $$status
	@if grep -E '^\s*--- SKIP' $(BUILD)/test.log; then echo "a skipped test fails: fix it or remove it" >&2; exit 1; fi
	@$(PY) scripts/go_coverage.py $(BUILD)/cover.out $(COVERAGE_FLOOR)

.PHONY: build
build:
	go build $(GO_PKGS)

.PHONY: vuln
vuln:
	$(GO_TOOL) $(GOVULNCHECK) $(GO_PKGS)

# The Go programs run by hand, outside the module's packages (`//go:build ignore`,
# under web/, which the Go checks leave out): web/e2e/store/make.go. Formatted,
# and vetted, which compiles them, so one that has rotted fails here (CI's test
# job) rather than when someone next runs it.
GO_SCRIPTS := web/e2e/store/make.go

.PHONY: go-scripts
go-scripts:
	@unformatted=$$(gofmt -l $(GO_SCRIPTS)) || exit 1; \
	if [ -n "$$unformatted" ]; then echo "not formatted (gofmt -w):" $$unformatted >&2; exit 1; fi
	@for f in $(GO_SCRIPTS); do go vet "$$f" || exit 1; done

.PHONY: links
links:
	$(PY) scripts/docs_links.py

.PHONY: guard
guard:
	$(PY) scripts/guard_copy.py

# The web app (web/, from v1 with those-who-run/paddock#120). Node is the version in
# web/.node-version, and the packages are web/package-lock.json's, installed with
# npm ci. A missing Node, or another version, fails rather than building with it.
# Playwright (web/e2e/) is web-e2e.
WEB_DEPS := web/node_modules/.package-lock.json

.PHONY: web-node
web-node:
	@want=$$(cat web/.node-version); \
	have=$$(node --version 2>/dev/null) || { echo "Node is missing: install Node $$want (web/.node-version)" >&2; exit 1; }; \
	[ "$$have" = "v$$want" ] || { echo "Node $$have is not web/.node-version's $$want: install it (nvm install $$want)" >&2; exit 1; }

$(WEB_DEPS): web/package.json web/package-lock.json
	@$(MAKE) --no-print-directory web-node
	cd web && npm ci --no-audit --no-fund
	@touch $@

.PHONY: web-deps
web-deps: $(WEB_DEPS)

.PHONY: web-build
web-build: web-node $(WEB_DEPS)
	cd web && npm run build

.PHONY: web-dev
web-dev: web-node $(WEB_DEPS)
	cd web && npm run dev

# ESLint, tsc, the generated client against the pinned contracts tag, Vitest, the build (into
# web/dist) and the bundle budget on it: CI's web job runs this target.
.PHONY: web-check
web-check: web-node $(WEB_DEPS)
	cd web && npm run lint
	cd web && npm run typecheck
	cd web && npm run api:check
	cd web && npm test
	cd web && npx vite build
	cd web && npm run budget

# The end-to-end tests (web/e2e/, those-who-run/paddock#130): web/dist on the Go API
# (built from this checkout), over NATS and the pinned logbook image serving
# web/e2e/store's data, in Chromium at the three viewports. Needs Go, Docker (or
# E2E_CONTAINER=podman) and Playwright's Chromium (`cd web && npx playwright install
# chromium`). Not part of make check: CI's e2e job runs it.
.PHONY: web-e2e
web-e2e: web-build
	cd web && npm run e2e

# The guide's screenshots (docs/pitwall.md; those-who-run/paddock#135): the docs
# config, web/playwright.docs.config.ts, over the same set-up as web-e2e, at the
# phone and desktop sizes, into docs/assets/screens/<viewport>/<page>.png. Needs
# what web-e2e needs. Not part of make check: CI's docs-screenshots workflow runs
# it and uploads the pictures, for someone to commit.
.PHONY: docs-screenshots
docs-screenshots: web-build
	cd web && npx playwright test -c playwright.docs.config.ts
