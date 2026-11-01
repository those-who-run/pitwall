package service

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/nats-io/nats.go"

	"github.com/those-who-run/pitwall/internal/api"
)

// The web app's caching (v1's server.py): Vite's hashed files under assets/ are
// kept for a year, and index.html is asked for again every time, so a deploy is
// never hidden behind a stale page.
const (
	cacheImmutable = "public, max-age=31536000, immutable"
	cacheNoCache   = "no-cache"
)

// gzipOver is the size of an answer, in bytes, above which it is gzipped when
// the browser takes it (v1's GZIP_OVER): a trace is about 60 KB, an error a few
// dozen bytes.
const gzipOver = 1024

// healthWait is how long /healthz waits on logbook, so a slow logbook never
// makes the probe time out (v1's HEALTH_ENGINEER_SECS).
const healthWait = 500 * time.Millisecond

// Health says whether the pit wall's dependencies answer, for /healthz.
type Health interface {
	// NATS is whether the NATS connection is up.
	NATS() bool
	// Logbook is whether logbook answers its micro ping.
	Logbook(ctx context.Context) bool
}

// natsHealth is Health over the pit wall's NATS connection.
type natsHealth struct{ nc *nats.Conn }

func (h natsHealth) NATS() bool { return h.nc.IsConnected() }

// Logbook pings logbook's micro service ($SRV.PING.logbook).
func (h natsHealth) Logbook(ctx context.Context) bool {
	if !h.nc.IsConnected() {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, healthWait)
	defer cancel()
	_, err := h.nc.RequestWithContext(ctx, "$SRV.PING.logbook", nil)
	return err == nil
}

// Deps is what Handler serves.
type Deps struct {
	// Version is the pit wall's, reported by /healthz.
	Version string
	// API is PitwallService.
	API *api.Server
	// StaticDir is the built web app; empty serves none.
	StaticDir string
	// Health reports NATS and logbook on /healthz.
	Health Health
	// Logger takes the access log, at debug.
	Logger *slog.Logger
}

// Handler serves everything on one origin (paddock#124 R9): PitwallService over
// Connect, with GET for every read, POST only for the two writes, and gzip over
// gzipOver; GET /healthz; and the web app, from StaticDir, on every other path.
// It answers no CORS preflight, and Connect takes only JSON or protobuf
// bodies, so an ordinary cross-site page can't write (crosssite_test.go). A
// DNS-rebinding page is same-origin to the browser and isn't stopped: ADR 0014
// point 8 in paddock accepts that, as a delete can be restored.
func Handler(d Deps) http.Handler {
	mux := http.NewServeMux()
	server := connect.NewServer()
	d.API.Register(server)
	connecthttp.Mount(mux, server, connecthttp.WithCompressMinBytes(gzipOver))
	mux.HandleFunc("GET /healthz", healthz(d))
	if d.StaticDir != "" {
		mux.Handle("/", static(d.StaticDir))
	}
	return accessLog(d.Logger, vary(mux))
}

// health is /healthz's body.
type health struct {
	OK      bool   `json:"ok"`
	Version string `json:"version"`
	NATS    bool   `json:"nats"`
	Logbook bool   `json:"logbook"`
}

// healthz answers 200 while the pit wall can serve, with whether NATS and
// logbook answered: either being down never fails it (v1: an engineer that is
// down never fails it). It is 503 only when the web app is configured and its
// index.html can't be read (paddock#124 R5).
func healthz(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := health{OK: true, Version: d.Version}
		if d.Health != nil {
			body.NATS = d.Health.NATS()
			body.Logbook = body.NATS && d.Health.Logbook(r.Context())
		}
		if d.StaticDir != "" {
			if _, err := os.Stat(path.Join(d.StaticDir, "index.html")); err != nil {
				body.OK = false
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if !body.OK {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		// An error here is the client gone; there is no one left to tell.
		_ = json.NewEncoder(w).Encode(body)
	}
}

// static serves the web app from dir: a file that is there as it is, with
// assets/ immutable and the rest no-cache, and index.html for any other path,
// so the app's client routes (/sessions/<id>, /_kit) load, and v1's results
// card link, /?session=<id>, opens its session (ADR 0009 and 0011: permanent).
// A missing file under assets/ is a 404, never index.html for a script, and so
// is a path under the API's or /api/.
func static(dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "the web app is read only", http.StatusMethodNotAllowed)
			return
		}
		rel := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if strings.HasPrefix(rel, "api/") || strings.HasPrefix(rel, "twr.") {
			http.NotFound(w, r)
			return
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			http.Error(w, "the web app is not there", http.StatusServiceUnavailable)
			return
		}
		defer func() { _ = root.Close() }()
		if rel != "" && serveFile(w, r, root.FS(), rel) {
			return
		}
		if strings.HasPrefix(rel, "assets/") || !serveFile(w, r, root.FS(), "index.html") {
			http.NotFound(w, r)
		}
	})
}

// serveFile serves name from fsys if it is a regular file, and says whether it
// did. os.Root keeps name inside the directory.
func serveFile(w http.ResponseWriter, r *http.Request, fsys fs.FS, name string) bool {
	f, err := fsys.Open(name)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	content, ok := f.(interface {
		Read([]byte) (int, error)
		Seek(int64, int) (int64, error)
	})
	if !ok {
		return false
	}
	cache := cacheNoCache
	if strings.HasPrefix(name, "assets/") {
		cache = cacheImmutable
	}
	w.Header().Set("Cache-Control", cache)
	http.ServeContent(w, r, info.Name(), info.ModTime(), content)
	return true
}

// vary tells caches an API answer differs by Accept-Encoding, as it is gzipped
// for a browser that takes it.
func vary(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/twr.") {
			w.Header().Add("Vary", "Accept-Encoding")
		}
		next.ServeHTTP(w, r)
	})
}

// statusWriter keeps the status a handler wrote, for the access log.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

// Unwrap lets http.ResponseController reach the writer underneath (Connect
// flushes through it).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// accessLog logs each request's method, path, status and time at debug, and
// never a header or the query (paddock#124 R7).
func accessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		logger.DebugContext(r.Context(), "http", "method", r.Method, "path", r.URL.Path,
			"status", sw.status, "ms", float64(time.Since(start).Microseconds())/1000)
	})
}
