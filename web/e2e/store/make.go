//go:build ignore

// make writes the end-to-end tests' logbook store again (those-who-run/paddock#130,
// the maintainer's decision D8, 2026-10-31): web/e2e/store/data, the data directory
// the pinned logbook image serves in CI, with no ecu there. It is run by hand, when
// a pin below changes or a spec needs another session, never in CI.
//
// For each of v1's recordings below it runs
//
//	ecu replay --nats URL --src SRC --seed SRC --clock CLOCK --speed max RECORDING
//
// against a NATS server of its own (paddock's streams and buckets, from
// web/e2e/nats) and keeps what ecu published there, in the order the server took
// it. Then it starts logbook, the image below, on an empty data directory and a
// second server, publishes those messages to it and waits for logbook to have
// taken them all, then stops it and keeps the directory. Each recording is a
// source of its own, so one store serves every pit wall the specs start: each
// lists the sources it shows in TWR_PITWALL_SRC (store.json says which).
//
// The --seed makes the session ids the same on every run, and the --clock dates
// each session. A store made again holds the same sessions, laps
// and facts, though not the same bytes.
//
// The open session (the live session page, #298 AC5 in v1): the pit stop's
// recording replayed with --keep-open, so ecu never ends it. Only its messages
// up to the end of lap LiveLapsAtStart go to logbook; those of the next lap are
// kept in live-lap.jsonl.gz, which the live spec publishes to CI's NATS as the
// engineer would commit the lap. store.json carries the sequence the EVENTS stream
// had reached, so CI's starts after it and logbook's last-wins guard takes the
// new lap's facts.
//
// ecu's status at the end of the replays, which v1's status route answered after
// a replay (paddock#125 AC5), is kept in store.json too, from the replay's own
// keys in the live bucket: the pit wall reads only the live source's keys, so a
// spec writes it there.
//
// From the repository's root, with a paddock clone at PADDOCK (its recordings/
// holds v1's fixtures, and SOURCES.yaml their sha256):
//
//	go run web/e2e/store/make.go -paddock PADDOCK
//
// nats-server is the one the Go tests download (TWR_TEST_NATS_SERVER, or the
// user's cache), or -nats-server. logbook runs in -container (docker or podman).
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// The pins: ecu, the version live when the store was made, and logbook, the image
// web/e2e/global-setup.ts runs in CI (it pins the same digest until Renovate bumps
// it there; a newer logbook migrates the store as it opens it).
const (
	ecuModule    = "github.com/those-who-run/ecu/cmd/ecu"
	ecuVersion   = "v0.1.15"
	logbookImage = "ghcr.io/those-who-run/twr-logbook:0.1.7@sha256:b4fe71257ed0a122cb6942072c1dcff8192c2ae0fd345b4a13b8ff852d85ffd0"
)

// LiveLapsAtStart is the laps the open session has in the store; the next one is
// in live-lap.jsonl.gz.
const LiveLapsAtStart = 4

// zeroLapSecs is how much of the race the session with no full lap keeps: its
// first minute, as v1's `those-who-run thin --end 60` cut it.
const zeroLapSecs = 60

// replay is one recording replayed into the store, under its own source.
type replay struct {
	Src       string `json:"src"`
	Recording string `json:"recording"`
	Clock     string `json:"clock"`
	// CutSecs keeps only the recording's first seconds (0 keeps it all).
	CutSecs float64 `json:"cut_secs,omitempty"`
	// KeepOpen replays with --keep-open, for the open session.
	KeepOpen bool `json:"keep_open,omitempty"`
}

// replays: v1's STORE_FIXTURES (the joined race, the time trial, the race, the
// pit stop), an hour apart, the open copy of the pit stop a second before it, then
// the race's first minute and the session with no stop. All on one day, as v1's
// stores were replayed on the day of the run: the specs fix the browser's clock
// later that day (web/e2e/pitwall.ts), so they are "Today". The last two are in
// other stores than the first five, so their being the newest changes no list.
var replays = []replay{
	{Src: "replay-e2e-joined", Recording: "real-nuc-suzuka-joined-mid-race-2026-10-18.jsonl.gz", Clock: "2026-10-15T09:00:00Z"},
	{Src: "replay-e2e-fuji", Recording: "real-nuc-fuji-timed-2026-10-15.jsonl.gz", Clock: "2026-10-15T10:00:00Z"},
	{Src: "replay-e2e-gilles", Recording: "real-nuc-gilles-villeneuve-race-2026-10-17.jsonl.gz", Clock: "2026-10-15T11:00:00Z"},
	{Src: "replay-e2e-live", Recording: "real-nuc-suzuka-pit-stop-2026-10-15.jsonl.gz", Clock: "2026-10-15T11:59:59Z", KeepOpen: true},
	{Src: "replay-e2e-pit-stop", Recording: "real-nuc-suzuka-pit-stop-2026-10-15.jsonl.gz", Clock: "2026-10-15T12:00:00Z"},
	{Src: "replay-e2e-zero-lap", Recording: "real-nuc-gilles-villeneuve-race-2026-10-17.jsonl.gz", Clock: "2026-10-15T12:30:00Z", CutSecs: zeroLapSecs},
	{Src: "replay-e2e-no-stop", Recording: "real-nuc-suzuka-no-stop-2026-10-15.jsonl.gz", Clock: "2026-10-15T12:45:00Z"},
}

// line is one message as ecu published it: logbook's replay fixtures' form.
type line struct {
	Subject string              `json:"subject"`
	Headers map[string][]string `json:"headers"`
	Payload json.RawMessage     `json:"payload"`
}

// manifest is store.json.
type manifest struct {
	About       string            `json:"about"`
	Ecu         string            `json:"ecu"`
	Logbook     string            `json:"logbook"`
	Replays     []replay          `json:"replays"`
	Sessions    map[string]string `json:"sessions"`
	LiveLaps    int               `json:"live_laps_at_start"`
	EventsSeq   uint64            `json:"events_seq"`
	FramesSeq   uint64            `json:"frames_seq"`
	EcuAtTheEnd struct {
		Src     string          `json:"src"`
		Status  json.RawMessage `json:"status"`
		Session json.RawMessage `json:"session"`
	} `json:"ecu_at_the_end"`
}

func main() {
	paddock := flag.String("paddock", "", "a paddock clone, whose recordings/ holds v1's fixtures")
	natsServer := flag.String("nats-server", defaultNATSServer(), "nats-server v2.15.1")
	container := flag.String("container", "docker", "the container runtime logbook runs in: docker or podman")
	out := flag.String("out", filepath.Join("web", "e2e", "store"), "where data/, live-lap.jsonl.gz and store.json go")
	flag.Parse()
	if *paddock == "" {
		fmt.Fprintln(os.Stderr, "usage: go run web/e2e/store/make.go -paddock PADDOCK")
		os.Exit(2)
	}
	if err := run(*paddock, *natsServer, *container, *out); err != nil {
		fmt.Fprintln(os.Stderr, "make:", err)
		os.Exit(1)
	}
}

func defaultNATSServer() string {
	if bin := os.Getenv("TWR_TEST_NATS_SERVER"); bin != "" {
		return bin
	}
	cache, _ := os.UserCacheDir()
	return filepath.Join(cache, "those-who-run", "nats-server-v2.15.1-"+runtime.GOOS+"-"+runtime.GOARCH, "nats-server")
}

func run(paddock, natsServer, container, out string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	tmp, err := os.MkdirTemp("", "e2e-store-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	install := exec.CommandContext(ctx, "go", "install", ecuModule+"@"+ecuVersion)
	install.Env = append(os.Environ(), "GOBIN="+tmp)
	install.Stdout, install.Stderr = os.Stderr, os.Stderr
	if err := install.Run(); err != nil {
		return fmt.Errorf("go install %s@%s: %w", ecuModule, ecuVersion, err)
	}
	sums, err := recordingSums(filepath.Join(paddock, "recordings", "SOURCES.yaml"))
	if err != nil {
		return err
	}

	// The replays, on a server of their own.
	capture, err := startNATS(ctx, natsServer, filepath.Join(tmp, "capture"))
	if err != nil {
		return err
	}
	defer capture.stop()
	if err := bootstrap(ctx, capture.js, 0); err != nil {
		return err
	}
	var m manifest
	m.About = "The end-to-end tests' logbook store, made by web/e2e/store/make.go: read it there."
	m.Ecu, m.Logbook, m.Replays, m.LiveLaps = ecuVersion, logbookImage, replays, LiveLapsAtStart
	m.Sessions = map[string]string{}
	published := map[string][]line{}
	for _, r := range replays {
		path := filepath.Join(paddock, "recordings", r.Recording)
		if err := checkSum(path, sums[r.Recording]); err != nil {
			return err
		}
		if r.CutSecs > 0 {
			if path, err = cut(path, filepath.Join(tmp, r.Src+".jsonl.gz"), r.CutSecs); err != nil {
				return err
			}
		}
		msgs, err := replayInto(ctx, capture, filepath.Join(tmp, "ecu"), r, path)
		if err != nil {
			return fmt.Errorf("%s: %w", r.Src, err)
		}
		published[r.Src] = msgs
		if sid := sessionOf(msgs); sid != "" {
			m.Sessions[r.Src] = sid
		}
	}
	// ecu's status once the last full replay has ended (paddock#125 AC5).
	last := replays[len(replays)-1].Src
	m.EcuAtTheEnd.Src = last
	if m.EcuAtTheEnd.Status, m.EcuAtTheEnd.Session, err = ecuKeys(ctx, capture.js, last); err != nil {
		return err
	}

	// logbook, on a second server, takes what the replays published.
	store, err := startNATS(ctx, natsServer, filepath.Join(tmp, "store"))
	if err != nil {
		return err
	}
	defer store.stop()
	if err := bootstrap(ctx, store.js, 0); err != nil {
		return err
	}
	data := filepath.Join(tmp, "data")
	if err := os.Mkdir(data, 0o755); err != nil {
		return err
	}
	stopLogbook, err := startLogbook(ctx, container, data, store.url)
	if err != nil {
		return err
	}
	var liveLap []line
	for _, r := range replays {
		msgs := published[r.Src]
		if r.KeepOpen {
			at := lapEnd(msgs, LiveLapsAtStart)
			next := lapEnd(msgs, LiveLapsAtStart+1)
			if at < 0 || next < 0 {
				return fmt.Errorf("%s has no lap %d or %d", r.Src, LiveLapsAtStart, LiveLapsAtStart+1)
			}
			msgs, liveLap = msgs[:at+1], msgs[at+1:next+1]
		}
		if err := publish(ctx, store.js, msgs); err != nil {
			return err
		}
	}
	if err := drained(ctx, store.js); err != nil {
		_ = stopLogbook()
		return err
	}
	if err := stopLogbook(); err != nil {
		return err
	}
	if m.EventsSeq, err = lastSeq(ctx, store.js, "EVENTS"); err != nil {
		return err
	}
	if m.FramesSeq, err = lastSeq(ctx, store.js, "TELEMETRY"); err != nil {
		return err
	}

	// Into out: the data directory, the open session's next lap and the manifest.
	if err := os.RemoveAll(filepath.Join(out, "data")); err != nil {
		return err
	}
	if err := copyDir(data, filepath.Join(out, "data")); err != nil {
		return err
	}
	if err := writeLines(filepath.Join(out, "live-lap.jsonl.gz"), liveLap); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "store.json"), append(b, '\n'), 0o644)
}

// recordingSums reads each recording's sha256 from paddock's SOURCES.yaml, whose
// entries are `- file: NAME` followed by `sha256: SUM`.
func recordingSums(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	re := regexp.MustCompile(`(?m)^\s*- file: (\S+)\n(?:\s+\w+: .*\n)*?\s+sha256: ([0-9a-f]{64})`)
	sums := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		sums[m[1]] = m[2]
	}
	return sums, nil
}

func checkSum(path, want string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if got := sha256.Sum256(b); want == "" || hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("%s has sha256 %x, SOURCES.yaml says %q", path, got, want)
	}
	return nil
}

// cut writes the recording's first secs, by its packets' t, to dst.
func cut(src, dst string, secs float64) (string, error) {
	f, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	sc := bufio.NewScanner(zr)
	sc.Buffer(nil, 1<<20)
	first := -1.0
	for sc.Scan() {
		var p struct {
			T float64 `json:"t"`
		}
		if err := json.Unmarshal(sc.Bytes(), &p); err != nil {
			return "", err
		}
		if first < 0 {
			first = p.T
		}
		if p.T-first > secs {
			break
		}
		if _, err := zw.Write(append(sc.Bytes(), '\n')); err != nil {
			return "", err
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return dst, os.WriteFile(dst, buf.Bytes(), 0o644)
}

type natsServer struct {
	url  string
	nc   *nats.Conn
	js   jetstream.JetStream
	stop func()
}

func startNATS(ctx context.Context, bin, dir string) (*natsServer, error) {
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	url := "nats://127.0.0.1:" + strconv.Itoa(port)
	srv := exec.CommandContext(ctx, bin, "-a", "127.0.0.1", "-p", strconv.Itoa(port), "-js", "-sd", dir)
	if err := srv.Start(); err != nil {
		return nil, err
	}
	kill := func() { _ = srv.Process.Kill(); _ = srv.Wait() }
	var nc *nats.Conn
	for range 100 {
		if nc, err = nats.Connect(url); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		kill()
		return nil, err
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		kill()
		return nil, err
	}
	return &natsServer{url: url, nc: nc, js: js, stop: func() { nc.Close(); kill() }}, nil
}

// bootstrap adds paddock's streams and buckets, from web/e2e/nats, as paddock's
// bootstrap.sh does.
func bootstrap(ctx context.Context, js jetstream.JetStream, firstSeq uint64) error {
	dir := filepath.Join("web", "e2e", "nats")
	for _, name := range []string{"events.json", "telemetry.json", "callouts.json"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		var cfg jetstream.StreamConfig
		if err := json.Unmarshal(b, &cfg); err != nil {
			return err
		}
		cfg.FirstSeq = firstSeq
		// A replay is a few MB; paddock's limits (1 and 2 GiB) would ask the
		// temporary directory's disk for room it may not have.
		cfg.MaxBytes = min(cfg.MaxBytes, 256<<20)
		if _, err := js.CreateStream(ctx, cfg); err != nil {
			return err
		}
	}
	for _, name := range []string{"live.json", "ecu.json"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		var def struct {
			Bucket       string `json:"bucket"`
			Description  string `json:"description"`
			History      uint8  `json:"history"`
			Replicas     int    `json:"replicas"`
			MaxValueSize int32  `json:"max_value_size"`
			MaxBytes     int64  `json:"max_bucket_size"`
		}
		if err := json.Unmarshal(b, &def); err != nil {
			return err
		}
		if _, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{
			Bucket: def.Bucket, Description: def.Description, History: def.History, Replicas: def.Replicas,
			MaxValueSize: def.MaxValueSize, MaxBytes: def.MaxBytes, Storage: jetstream.FileStorage,
		}); err != nil {
			return err
		}
	}
	return nil
}

// replayInto runs ecu's replay of path against s and returns what it published to
// the streams, in the order the server took it.
func replayInto(ctx context.Context, s *natsServer, ecu string, r replay, path string) ([]line, error) {
	msgs := make(chan *nats.Msg, 1<<20)
	sub, err := s.nc.ChanSubscribe("twr.v1."+r.Src+".>", msgs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sub.Unsubscribe() }()
	if err := s.nc.Flush(); err != nil {
		return nil, err
	}
	args := []string{"replay", "--nats", s.url, "--src", r.Src, "--seed", r.Src, "--clock", r.Clock, "--speed", "max"}
	if r.KeepOpen {
		args = append(args, "--keep-open")
	}
	cmd := exec.CommandContext(ctx, ecu, append(args, path)...)
	cmd.Stdout, cmd.Stderr = io.Discard, os.Stderr
	cmd.Env = append(os.Environ(), "LOG_LEVEL=warn")
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ecu replay: %w", err)
	}
	if err := s.nc.Flush(); err != nil {
		return nil, err
	}
	var stored uint64
	for _, name := range []string{"EVENTS", "TELEMETRY"} {
		st, err := s.js.Stream(ctx, name)
		if err != nil {
			return nil, err
		}
		info, err := st.Info(ctx, jetstream.WithSubjectFilter("twr.v1."+r.Src+".>"))
		if err != nil {
			return nil, err
		}
		for _, n := range info.State.Subjects {
			stored += n
		}
	}
	var out []line
	for len(msgs) > 0 {
		m := <-msgs
		// What the streams hold, which is what logbook reads: not the 20 Hz copy.
		if !strings.HasSuffix(m.Subject, ".frame.gt7") && !strings.Contains(m.Subject, ".evt.") {
			continue
		}
		var payload bytes.Buffer
		if err := json.Compact(&payload, m.Data); err != nil {
			return nil, fmt.Errorf("%s: %w", m.Subject, err)
		}
		out = append(out, line{Subject: m.Subject, Headers: m.Header, Payload: payload.Bytes()})
	}
	if uint64(len(out)) != stored {
		return nil, fmt.Errorf("captured %d messages, the streams stored %d", len(out), stored)
	}
	return out, nil
}

// sessionOf is the session the messages started: twr.v1.<src>.evt.<sid>.session.started.
func sessionOf(msgs []line) string {
	for _, m := range msgs {
		if strings.HasSuffix(m.Subject, ".session.started") {
			return strings.Split(m.Subject, ".")[4]
		}
	}
	return ""
}

// lapEnd is the index of the lap.completed event for lap n, or -1.
func lapEnd(msgs []line, n int) int {
	for i, m := range msgs {
		if !strings.HasSuffix(m.Subject, ".lap.completed") {
			continue
		}
		var lap struct {
			Lap json.Number `json:"lap"`
		}
		if err := json.Unmarshal(m.Payload, &lap); err != nil {
			continue
		}
		if lap.Lap.String() == strconv.Itoa(n) {
			return i
		}
	}
	return -1
}

// ecuKeys is ecu's status and session keys for src in the live bucket (the
// session's is null once ecu has deleted it).
func ecuKeys(ctx context.Context, js jetstream.JetStream, src string) (json.RawMessage, json.RawMessage, error) {
	kv, err := js.KeyValue(ctx, "live")
	if err != nil {
		return nil, nil, err
	}
	status, err := kv.Get(ctx, "ecu.status."+src)
	if err != nil {
		return nil, nil, fmt.Errorf("ecu.status.%s: %w", src, err)
	}
	session := json.RawMessage("null")
	if e, err := kv.Get(ctx, "ecu.session."+src); err == nil {
		session = e.Value()
	} else if !errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil, nil, err
	}
	return status.Value(), session, nil
}

// startLogbook runs logbook's image on data and url, as the user running this so
// the directory stays theirs, and returns how to stop it.
func startLogbook(ctx context.Context, container, data, url string) (func() error, error) {
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	name := "e2e-store-logbook-" + strconv.Itoa(port)
	args := []string{"run", "--rm", "--detach", "--name", name, "--network", "host",
		"--user", strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()),
		"--volume", data + ":/data",
		"--env", "TWR_DATA_DIR=/data", "--env", "TWR_NATS_URL=" + url,
		"--env", "TWR_HEALTH_ADDR=127.0.0.1:" + strconv.Itoa(port)}
	if container == "podman" {
		// Rootless podman: the user's own ids inside, and the directory labelled
		// for the container where SELinux enforces.
		args = append(args, "--userns", "keep-id")
		args[slices.Index(args, data+":/data")] = data + ":/data:z"
	}
	cmd := exec.CommandContext(ctx, container, append(args, logbookImage)...)
	cmd.Stdout, cmd.Stderr = io.Discard, os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s run logbook: %w", container, err)
	}
	stop := func() error {
		c := exec.CommandContext(context.WithoutCancel(ctx), container, "stop", "--time", "10", name)
		c.Stdout, c.Stderr = io.Discard, os.Stderr
		return c.Run()
	}
	health := "http://127.0.0.1:" + strconv.Itoa(port) + "/healthz"
	for range 300 {
		if resp, err := http.Get(health); err == nil { //nolint:gosec // a local address
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return stop, nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = stop()
	return nil, errors.New("logbook did not answer /healthz within 30 s")
}

// publish sends msgs to JetStream as they were captured, headers and payload
// unchanged, and waits for the server to store them all.
func publish(ctx context.Context, js jetstream.JetStream, msgs []line) error {
	for _, m := range msgs {
		if _, err := js.PublishMsgAsync(&nats.Msg{Subject: m.Subject, Header: nats.Header(m.Headers), Data: m.Payload}); err != nil {
			return err
		}
	}
	select {
	case <-js.PublishAsyncComplete():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// drained waits until logbook's two consumers have nothing pending or unacked.
func drained(ctx context.Context, js jetstream.JetStream) error {
	until := time.Now().Add(5 * time.Minute)
	for time.Now().Before(until) {
		done := true
		for stream, consumer := range map[string]string{"EVENTS": "logbook-events", "TELEMETRY": "logbook-frames"} {
			c, err := js.Consumer(ctx, stream, consumer)
			if err != nil {
				done = false
				continue
			}
			info, err := c.Info(ctx)
			if err != nil || info.NumPending > 0 || info.NumAckPending > 0 {
				done = false
			}
		}
		if done {
			// The last batch acked is written; give the last lap's write a moment.
			time.Sleep(2 * time.Second)
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return errors.New("logbook did not take every message within 5 min")
}

func lastSeq(ctx context.Context, js jetstream.JetStream, name string) (uint64, error) {
	s, err := js.Stream(ctx, name)
	if err != nil {
		return 0, err
	}
	info, err := s.Info(ctx)
	if err != nil {
		return 0, err
	}
	return info.State.LastSeq, nil
}

func writeLines(path string, msgs []line) error {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(zw)
	for _, l := range msgs {
		if err := enc.Encode(l); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// copyDir copies src's files to dst, leaving out logbook's lock file. The index
// goes gzipped, as index.duckdb.gz: DuckDB's file is mostly empty blocks, over the
// commit hooks' 1 MB limit as it stands and about a sixth of it gzipped. The
// end-to-end set-up unzips it into its copy of the directory.
func copyDir(src, dst string) error {
	var names []string
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		names = append(names, path)
		return nil
	})
	if err != nil {
		return err
	}
	slices.Sort(names)
	for _, path := range names {
		rel, _ := filepath.Rel(src, path)
		if rel == "logbook.lock" {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if rel == "index.duckdb" {
			var buf bytes.Buffer
			zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
			if err != nil {
				return err
			}
			if _, err := zw.Write(b); err != nil {
				return err
			}
			if err := zw.Close(); err != nil {
				return err
			}
			rel, b = rel+".gz", buf.Bytes()
		}
		if err := os.MkdirAll(filepath.Join(dst, filepath.Dir(rel)), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, rel), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = ln.Close() }()
	if a, ok := ln.Addr().(*net.TCPAddr); ok {
		return a.Port, nil
	}
	return 0, errors.New("no TCP port")
}
