// Package service is the pit wall's server (paddock#124), kept to the conventions
// every Go component starts from (README.md, "Conventions"): configuration from
// the environment, log/slog writing JSON, and a health endpoint. On one origin it
// serves PitwallService over Connect (internal/api), GET /healthz, and the built
// web app from a directory.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/those-who-run/pitwall/internal/api"
	"github.com/those-who-run/pitwall/internal/engine"
)

// DefaultSources is TWR_PITWALL_SRC when unset: v2's own sessions and v1's
// imported history (the maintainer's decision D7, 2026-10-31).
var DefaultSources = []string{"live", "v1-import"}

// Config is the service's configuration, read from the environment by
// ConfigFromEnv. Its own settings carry the TWR_ prefix (the maintainer's
// decision D5, 2026-10-31); ADDR and LOG_LEVEL are the chassis's, as in every
// component.
type Config struct {
	// Addr is the address the HTTP server listens on: ADDR, ":8080" when unset.
	Addr string
	// LogLevel is the lowest level logged: LOG_LEVEL (debug, info, warn or error),
	// info when unset.
	LogLevel slog.Level
	// NATSURL is the NATS server logbook and the live KV are on: TWR_NATS_URL,
	// nats://127.0.0.1:4222 when unset.
	NATSURL string
	// StaticDir is the built web app's directory: TWR_PITWALL_STATIC_DIR. Unset
	// serves no web app, only the API and /healthz.
	StaticDir string
	// Sources are the sources whose sessions the pit wall shows, in logbook's
	// order of preference: TWR_PITWALL_SRC, comma separated, DefaultSources when
	// unset. A list with no source in it is an error, never "every source".
	Sources []string
	// Stale is how old ecu's live status may be before the chip says the
	// engineer is offline: TWR_PITWALL_STALE, a Go duration such as 15s,
	// engine.DefaultStale when unset.
	Stale time.Duration
}

// ConfigFromEnv reads the configuration through getenv, which is os.Getenv outside
// the tests. An unset or empty variable takes its default; a value that can't be
// read is an error, so a typo stops the service rather than being ignored.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	cfg := Config{
		Addr:      ":8080",
		LogLevel:  slog.LevelInfo,
		NATSURL:   nats.DefaultURL,
		StaticDir: getenv("TWR_PITWALL_STATIC_DIR"),
		Sources:   DefaultSources,
		Stale:     engine.DefaultStale,
	}
	if addr := getenv("ADDR"); addr != "" {
		cfg.Addr = addr
	}
	if level := getenv("LOG_LEVEL"); level != "" {
		if err := cfg.LogLevel.UnmarshalText([]byte(level)); err != nil {
			return Config{}, fmt.Errorf("LOG_LEVEL: %w", err)
		}
	}
	if url := getenv("TWR_NATS_URL"); url != "" {
		cfg.NATSURL = url
	}
	if src := getenv("TWR_PITWALL_SRC"); src != "" {
		cfg.Sources = nil
		for s := range strings.SplitSeq(src, ",") {
			if s = strings.TrimSpace(s); s != "" {
				cfg.Sources = append(cfg.Sources, s)
			}
		}
		if len(cfg.Sources) == 0 {
			return Config{}, fmt.Errorf("TWR_PITWALL_SRC: %q names no source", src)
		}
	}
	if stale := getenv("TWR_PITWALL_STALE"); stale != "" {
		d, err := time.ParseDuration(stale)
		if err != nil || d <= 0 {
			return Config{}, fmt.Errorf("TWR_PITWALL_STALE: %q is not a duration above zero, such as 15s", stale)
		}
		cfg.Stale = d
	}
	return cfg, nil
}

// NewLogger returns a logger that writes JSON lines to w, one object per record,
// from level up.
func NewLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

// shutdownGrace is how long Run lets requests in flight finish once ctx is done:
// a rollout never waits on the pit wall (paddock#124 R8, v1's SHUTDOWN_SECS).
const shutdownGrace = time.Second

// The server's limits, for a small service whose requests are short: a client
// that is slow to send its headers or its whole request, or slow to read the
// response, is cut off rather than holding a connection, and an idle keep-alive
// connection is closed after a minute.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 10 * time.Second
	idleTimeout       = 60 * time.Second
)

// Run connects to NATS, listens on cfg.Addr and serves until ctx is done, then
// shuts the server down gracefully. NATS not answering at the start does not
// stop it: the connection keeps trying, the data methods answer unavailable
// and the chip offline meanwhile, and the web app is served (paddock#124 R3).
// It returns nil after a clean shutdown, or why it could not listen or serve.
func Run(ctx context.Context, cfg Config, logger *slog.Logger, version string) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	nc, err := nats.Connect(cfg.NATSURL,
		nats.Name("pitwall"),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
	)
	if err != nil {
		_ = ln.Close()
		return fmt.Errorf("nats: %w", err)
	}
	defer nc.Close()
	watcher := engine.New(nc, cfg.Stale, logger)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go watcher.Run(ctx)
	deps := Deps{
		Version:   version,
		API:       api.New(nc, cfg.Sources, version, watcher, 0),
		StaticDir: cfg.StaticDir,
		Health:    natsHealth{nc: nc},
		Logger:    logger,
	}
	return serve(ctx, ln, Handler(deps), logger, version)
}

// newServer is the HTTP server for h, with the limits above, logging its own errors
// through logger.
func newServer(h http.Handler, logger *slog.Logger) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
}

func serve(ctx context.Context, ln net.Listener, h http.Handler, logger *slog.Logger, version string) error {
	srv := newServer(h, logger)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	logger.InfoContext(ctx, "serving", "addr", ln.Addr().String(), "version", version)

	select {
	case err := <-served:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}
	logger.InfoContext(ctx, "shutting down")
	// ctx is already done, so the grace period needs a context of its own.
	grace, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	defer cancel()
	return shutdown(grace, srv, served)
}

// shutdown stops srv gracefully within ctx and waits for its Serve to return the
// error that served carries. Once Shutdown is called Serve returns
// http.ErrServerClosed, which is a clean stop. Anything else is an error Serve hit
// on its own, as ctx was done, and is not lost: it is returned with any error from
// Shutdown.
func shutdown(ctx context.Context, srv *http.Server, served <-chan error) error {
	var errs []error
	if err := srv.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("shutdown: %w", err))
	}
	if err := <-served; err != nil && !errors.Is(err, http.ErrServerClosed) {
		errs = append(errs, fmt.Errorf("serve: %w", err))
	}
	return errors.Join(errs...)
}
