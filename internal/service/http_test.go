package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/those-who-run/pitwall/internal/api"
	"github.com/those-who-run/pitwall/internal/logbooktest"
	"github.com/those-who-run/pitwall/internal/natstest"
)

// closedAddress is an address nothing listens on, so connecting fails at once.
func closedAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// testHandler is Handler with d's gaps filled: an API over a NATS connection
// that answers nothing, and a logger that drops everything.
func testHandler(t *testing.T, d Deps) http.Handler {
	t.Helper()
	if d.API == nil {
		nc, err := nats.Connect("nats://"+closedAddress(t), nats.RetryOnFailedConnect(true), nats.MaxReconnects(-1))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(nc.Close)
		d.API = api.New(nc, DefaultSources, "test", nil, 100*time.Millisecond)
	}
	if d.Logger == nil {
		d.Logger = NewLogger(io.Discard, slog.LevelInfo)
	}
	if d.Version == "" {
		d.Version = "1.2.3"
	}
	return Handler(d)
}

func serveReq(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

type fakeHealth struct{ nats, logbook bool }

func (f fakeHealth) NATS() bool                   { return f.nats }
func (f fakeHealth) Logbook(context.Context) bool { return f.logbook }

func healthBody(t *testing.T, rec *httptest.ResponseRecorder) health {
	t.Helper()
	var body health
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

// R5: /healthz is 200 whether or not NATS and logbook answer, and says which.
func TestHealthz(t *testing.T) {
	for _, h := range []fakeHealth{{true, true}, {true, false}, {false, true}} {
		rec := serveReq(testHandler(t, Deps{Health: h}), http.MethodGet, "/healthz")
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("%v: status %d, Content-Type %q", h, rec.Code, rec.Header().Get("Content-Type"))
		}
		want := health{OK: true, Version: "1.2.3", NATS: h.nats, Logbook: h.nats && h.logbook}
		if got := healthBody(t, rec); got != want {
			t.Errorf("%v: body %+v, want %+v", h, got, want)
		}
	}
	if got := healthBody(t, serveReq(testHandler(t, Deps{}), http.MethodGet, "/healthz")); !got.OK || got.NATS {
		t.Errorf("with no Health: %+v", got)
	}
}

func TestHealthzIsGetOnly(t *testing.T) {
	if rec := serveReq(testHandler(t, Deps{}), http.MethodPost, "/healthz"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /healthz status = %d, want 405", rec.Code)
	}
}

// R5: a web app that is configured but not there is the one 503.
func TestHealthzIs503WithoutTheWebApp(t *testing.T) {
	rec := serveReq(testHandler(t, Deps{StaticDir: filepath.Join(t.TempDir(), "missing")}), http.MethodGet, "/healthz")
	if rec.Code != http.StatusServiceUnavailable || healthBody(t, rec).OK {
		t.Errorf("status %d, body %s", rec.Code, rec.Body)
	}
	if rec := serveReq(testHandler(t, Deps{StaticDir: filepath.Join(t.TempDir(), "missing")}), http.MethodGet, "/sessions/s"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a page with no web app: %d, want 503", rec.Code)
	}
}

// The real Health: NATS up, and logbook answering its micro ping or not.
func TestNATSHealth(t *testing.T) {
	s := natstest.Bare(t)
	nc, err := nats.Connect(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	h := natsHealth{nc: nc}
	if !h.NATS() || h.Logbook(t.Context()) {
		t.Errorf("no logbook: NATS %v, logbook %v", h.NATS(), h.Logbook(t.Context()))
	}
	logbooktest.Start(t, nc)
	if !h.Logbook(t.Context()) {
		t.Error("logbook answers, but Logbook is false")
	}
	nc.Close()
	if h.NATS() || h.Logbook(t.Context()) {
		t.Error("a closed connection is up")
	}
}

func webApp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"index.html":           "<!doctype html><title>pit wall</title>",
		"assets/index-abc.js":  "console.log(1)",
		"favicon.svg":          "<svg/>",
		"assets/nested/dir/.k": "",
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// AC3 and R4: client routes and v1's results-card link are index.html, not
// cached; a hashed asset is immutable; a missing asset is a 404.
func TestTheWebApp(t *testing.T) {
	h := testHandler(t, Deps{StaticDir: webApp(t)})
	for _, c := range []struct {
		path, body, cache string
		status            int
	}{
		{"/", "pit wall", "no-cache", 200},
		{"/?session=20261017-162118", "pit wall", "no-cache", 200},
		{"/sessions/abc", "pit wall", "no-cache", 200},
		{"/sessions/abc/compare?a=1&b=2", "pit wall", "no-cache", 200},
		{"/_kit", "pit wall", "no-cache", 200},
		{"/index.html", "pit wall", "no-cache", 200},
		{"/favicon.svg", "<svg/>", "no-cache", 200},
		{"/assets/index-abc.js", "console.log(1)", "public, max-age=31536000, immutable", 200},
		{"/assets/index-old.js", "", "", 404},
		{"/assets/nested", "", "", 404},
		{"/etc/passwd", "pit wall", "no-cache", 200},
		{"/api/v1/sessions", "", "", 404},
		{"/twr.other.v1.Service/Method", "", "", 404},
	} {
		rec := serveReq(h, http.MethodGet, c.path)
		if rec.Code != c.status || !strings.Contains(rec.Body.String(), c.body) || rec.Header().Get("Cache-Control") != c.cache {
			t.Errorf("GET %s: %d, Cache-Control %q, body %q", c.path, rec.Code, rec.Header().Get("Cache-Control"), rec.Body)
		}
	}
	if rec := serveReq(h, http.MethodPost, "/sessions/abc"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST to the web app: %d, want 405", rec.Code)
	}
	// The SPA's index.html there no more: nothing to fall back to.
	dir := webApp(t)
	if err := os.Remove(filepath.Join(dir, "index.html")); err != nil {
		t.Fatal(err)
	}
	if rec := serveReq(testHandler(t, Deps{StaticDir: dir}), http.MethodGet, "/sessions/abc"); rec.Code != http.StatusNotFound {
		t.Errorf("no index.html: %d, want 404", rec.Code)
	}
	if rec := serveReq(testHandler(t, Deps{}), http.MethodGet, "/"); rec.Code != http.StatusNotFound {
		t.Errorf("no web app configured: GET / %d, want 404", rec.Code)
	}
}

// AC4: with logbook gone a data method answers unavailable, and the web app is
// still served.
func TestTheWebAppOutlivesLogbook(t *testing.T) {
	srv := httptest.NewServer(testHandler(t, Deps{StaticDir: webApp(t)}))
	t.Cleanup(srv.Close)
	start := time.Now()
	resp, err := http.Post(srv.URL+"/twr.pitwall.v1.PitwallService/GetSession", "application/json", strings.NewReader(`{"sessionId":"s"}`)) //nolint:noctx // a test
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || !bytes.Contains(body, []byte(`"unavailable"`)) || time.Since(start) > 2500*time.Millisecond {
		t.Errorf("GetSession with no logbook: %d %s after %v", resp.StatusCode, body, time.Since(start))
	}
	if resp.Header.Get("Vary") != "Accept-Encoding" {
		t.Errorf("an API answer's Vary is %q", resp.Header.Get("Vary"))
	}
	if code := get(t, srv.URL+"/"); code != http.StatusOK {
		t.Errorf("GET / with no logbook: %d", code)
	}
}

// R7: the access log has method, path, status and time at debug, and never a
// header or the query.
func TestTheAccessLogNeverLogsAHeader(t *testing.T) {
	var logs bytes.Buffer
	h := testHandler(t, Deps{StaticDir: webApp(t), Logger: NewLogger(&logs, slog.LevelDebug)})
	req := httptest.NewRequest(http.MethodGet, "/sessions/abc?token=sekrit", nil)
	req.Header.Set("Authorization", "Bearer sekrit")
	h.ServeHTTP(httptest.NewRecorder(), req)
	serveReq(h, http.MethodGet, "/assets/missing.js")
	line := logs.String()
	if strings.Contains(line, "sekrit") {
		t.Errorf("the log has a secret: %s", line)
	}
	for _, want := range []string{`"msg":"http"`, `"method":"GET"`, `"path":"/sessions/abc"`, `"status":200`, `"status":404`, `"ms":`} {
		if !strings.Contains(line, want) {
			t.Errorf("the log has no %s: %s", want, line)
		}
	}
	// A handler that writes nothing is a 200.
	logs.Reset()
	accessLog(NewLogger(&logs, slog.LevelDebug), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(logs.String(), `"status":200`) {
		t.Errorf("an empty answer: %s", logs.String())
	}
}

// R8 and AC6: a rollout never waits more than a second on the pit wall.
func TestShutdownGraceIsASecond(t *testing.T) {
	if shutdownGrace != time.Second {
		t.Errorf("shutdownGrace = %v, want 1 s", shutdownGrace)
	}
}
