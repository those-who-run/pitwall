// Package engine is the engineer's status for the shell's live chip
// (paddock#125): a watch on ecu's and radio's values in the live KV bucket, kept
// open for the server's life, so GetEngineStatus answers the latest value with
// no wait on NATS. It reads only the live source's keys, so a replay on the same
// bus never shows (R4). With no value, a stale one or NATS down it answers
// ACTIVITY_OFFLINE, never an error (R2, R3).
package engine

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pitwallv1 "github.com/those-who-run/contracts/gen/go/twr/pitwall/v1"
	statev1 "github.com/those-who-run/contracts/gen/go/twr/state/v1"

	"github.com/those-who-run/pitwall/internal/api"
)

// Bucket is the KV bucket ecu and radio keep their live state in.
const Bucket = "live"

// The keys read: ecu's status and open session, and radio's status, for the live
// source only (contracts' subjects.yaml).
const (
	KeyEcuStatus   = "ecu.status.live"
	KeyEcuSession  = "ecu.session.live"
	KeyRadioStatus = "radio.status.live"
)

// DefaultStale is how old ecu's status may be before ecu counts as gone: ecu
// rewrites it every second while idle and at least every 5 s while busy.
const DefaultStale = 15 * time.Second

// retry is how long the watch waits before trying again after it could not
// start or it stopped.
const retry = time.Second

// entry is a value as the watch last saw it, and when NATS stored it.
type entry struct {
	msg proto.Message
	at  time.Time
}

// Watcher keeps the latest of each key. Its zero value is not usable: use New.
type Watcher struct {
	stale  time.Duration
	now    func() time.Time
	nc     *nats.Conn
	logger *slog.Logger

	mu     sync.Mutex
	values map[string]entry
}

// New is a watcher on nc's live bucket; a value older than stale is gone (0 is
// DefaultStale). Run starts it.
func New(nc *nats.Conn, stale time.Duration, logger *slog.Logger) *Watcher {
	if stale <= 0 {
		stale = DefaultStale
	}
	return &Watcher{stale: stale, now: time.Now, nc: nc, logger: logger, values: map[string]entry{}}
}

// Run watches the keys until ctx is done, starting the watch again whenever the
// bucket is not there yet, the watch stops, or nothing has come for the stale
// limit (a watch that has gone quiet is not trusted: ecu writes every second).
func (w *Watcher) Run(ctx context.Context) {
	for ctx.Err() == nil {
		quiet, err := w.watch(ctx)
		if err != nil && ctx.Err() == nil {
			w.logger.DebugContext(ctx, "live watch", "err", err)
		}
		if quiet {
			continue
		}
		select {
		case <-ctx.Done():
		case <-time.After(retry):
		}
	}
}

// watch watches until ctx is done or the watch stops, and says whether it
// stopped for having gone quiet, to be started again at once.
func (w *Watcher) watch(ctx context.Context) (bool, error) {
	js, err := jetstream.New(w.nc)
	if err != nil {
		return false, err
	}
	kv, err := js.KeyValue(ctx, Bucket)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	watcher, err := kv.WatchFiltered(ctx, []string{KeyEcuStatus, KeyEcuSession, KeyRadioStatus})
	if err != nil {
		return false, err
	}
	defer func() { _ = watcher.Stop() }()
	quiet := time.NewTimer(w.stale)
	defer quiet.Stop()
	for {
		select {
		case <-ctx.Done():
			return false, nil
		case <-quiet.C:
			return true, nil
		case e, ok := <-watcher.Updates():
			if !ok {
				return false, nil
			}
			if e == nil { // the values there were at the start have all come
				continue
			}
			quiet.Reset(w.stale)
			w.put(ctx, e)
		}
	}
}

// put keeps the entry's value, or forgets the key when it was deleted (ecu
// deletes its session key when the session ends).
func (w *Watcher) put(ctx context.Context, e jetstream.KeyValueEntry) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if e.Operation() != jetstream.KeyValuePut {
		delete(w.values, e.Key())
		return
	}
	var msg proto.Message
	switch e.Key() {
	case KeyEcuStatus:
		msg = &statev1.EcuStatus{}
	case KeyEcuSession:
		msg = &statev1.LiveSession{}
	case KeyRadioStatus:
		msg = &statev1.RadioStatus{}
	default:
		return
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(e.Value(), msg); err != nil {
		w.logger.WarnContext(ctx, "live value unreadable", "key", e.Key(), "err", err)
		delete(w.values, e.Key())
		return
	}
	w.values[e.Key()] = entry{msg: msg, at: e.Created()}
}

// fresh is the key's value while it is younger than the stale limit.
func (w *Watcher) fresh(key string) proto.Message {
	e, ok := w.values[key]
	if !ok || w.now().Sub(e.at) > w.stale {
		return nil
	}
	return e.msg
}

// Status is the engineer's status now (api.Engine).
func (w *Watcher) Status() *pitwallv1.EngineStatus {
	offline := &pitwallv1.EngineStatus{Activity: pitwallv1.Activity_ACTIVITY_OFFLINE}
	if w.nc != nil && !w.nc.IsConnected() {
		return offline
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	ecu, _ := w.fresh(KeyEcuStatus).(*statev1.EcuStatus)
	if ecu == nil {
		return offline
	}
	session, _ := w.fresh(KeyEcuSession).(*statev1.LiveSession)
	radio, _ := w.fresh(KeyRadioStatus).(*statev1.RadioStatus)
	return Status(ecu, session, radio)
}

// activities maps ecu's activity to the API's.
var activities = map[statev1.Activity]pitwallv1.Activity{
	statev1.Activity_ACTIVITY_IDLE:    pitwallv1.Activity_ACTIVITY_IDLE,
	statev1.Activity_ACTIVITY_DRIVING: pitwallv1.Activity_ACTIVITY_DRIVING,
	statev1.Activity_ACTIVITY_PAUSED:  pitwallv1.Activity_ACTIVITY_PAUSED,
	statev1.Activity_ACTIVITY_REPLAY:  pitwallv1.Activity_ACTIVITY_REPLAY,
	statev1.Activity_ACTIVITY_DEMO:    pitwallv1.Activity_ACTIVITY_DEMO,
}

// Status is ecu's status, its open session (nil for none) and radio's status
// (nil when radio says nothing) as the API's EngineStatus (v1's status route).
// Nothing of ecu's run id, packets or radio's listeners goes out: no secret, PS5
// address or Discord id is in either value, and only the chip's fields are
// copied (R5).
func Status(ecu *statev1.EcuStatus, session *statev1.LiveSession, radio *statev1.RadioStatus) *pitwallv1.EngineStatus {
	out := &pitwallv1.EngineStatus{
		Version:   ecu.GetVersion(),
		StartedAt: ecu.GetStartedAt(),
		Activity:  activities[ecu.GetActivity()],
		IdleSecs:  ecu.GetIdleSecs(),
		Telemetry: &pitwallv1.TelemetryLink{
			Connected:         ecu.GetTelemetryConnected(),
			LastPacketAgeSecs: ecu.LastPacketAgeSecs,
		},
	}
	// Absent when radio says nothing of the voice (R6): "Voice: unknown".
	if radio != nil {
		out.VoiceConnected = radio.VoiceConnected
	}
	if session.GetSessionId() != "" {
		out.Session = &pitwallv1.LiveSession{
			SessionId: session.GetSessionId(),
			Kind:      api.KindKey(session.GetSessionType()),
			Lap:       session.Lap,
			TotalLaps: session.TotalLaps,
			Position:  session.Position,
			TotalCars: session.TotalCars,
		}
		if car := session.GetCarName(); car != "" {
			out.Session.Car = &car
		}
		if t := session.GetTrack(); t != nil {
			out.Session.Track = &pitwallv1.LiveTrack{TrackId: t.TrackId, Sure: t.GetSure()}
			if name := t.GetName(); name != "" {
				out.Session.Track.Name = &name
			}
		}
	}
	return out
}
