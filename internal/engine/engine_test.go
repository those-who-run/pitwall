package engine

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/those-who-run/contracts/gen/go/twr/events/v1"
	pitwallv1 "github.com/those-who-run/contracts/gen/go/twr/pitwall/v1"
	statev1 "github.com/those-who-run/contracts/gen/go/twr/state/v1"

	"github.com/those-who-run/pitwall/internal/natstest"
)

func ptr[T any](v T) *T { return &v }

var started = timestamppb.New(time.Date(2026, 10, 18, 9, 2, 41, 0, time.UTC))

// The live values of contracts' example: ecu 0.1.14 driving lap 3 of 6 at
// Suzuka East, with radio in the voice channel.
func driving() (*statev1.EcuStatus, *statev1.LiveSession, *statev1.RadioStatus) {
	ecu := &statev1.EcuStatus{
		Source: "live", RunId: "run-1", Version: "0.1.14", StartedAt: started,
		Activity: statev1.Activity_ACTIVITY_DRIVING, TelemetryConnected: true, LastPacketAgeSecs: ptr(0.0), Packets: 9000,
	}
	session := &statev1.LiveSession{
		SessionId: "01JB9QZ4V3X8M2K7T5R1N6PDWA", SessionType: eventsv1.SessionType_SESSION_TYPE_LAP_RACE,
		Track:   &statev1.LiveTrack{TrackId: ptr[int32](442), Name: "Suzuka Circuit East Course", Sure: true},
		CarName: "Honda Civic Si Group A version '87", Lap: ptr[int32](3), TotalLaps: ptr[int32](6),
		Position: ptr[int32](1), TotalCars: ptr[int32](2),
	}
	radio := &statev1.RadioStatus{
		Source: "live", Sink: statev1.RadioSink_RADIO_SINK_DISCORD, VoiceConnected: ptr(true), VoiceListeners: ptr[int32](1),
	}
	return ecu, session, radio
}

// want is contracts' examples/pitwall/v1/get_engine_status_response.json.
var want = &pitwallv1.EngineStatus{
	Version: "0.1.14", StartedAt: started, Activity: pitwallv1.Activity_ACTIVITY_DRIVING,
	Telemetry:      &pitwallv1.TelemetryLink{Connected: true, LastPacketAgeSecs: ptr(0.0)},
	VoiceConnected: ptr(true),
	Session: &pitwallv1.LiveSession{
		SessionId: "01JB9QZ4V3X8M2K7T5R1N6PDWA", Kind: "lap_race",
		Track: &pitwallv1.LiveTrack{TrackId: ptr[int32](442), Name: ptr("Suzuka Circuit East Course"), Sure: true},
		Car:   ptr("Honda Civic Si Group A version '87"), Lap: ptr[int32](3), TotalLaps: ptr[int32](6),
		Position: ptr[int32](1), TotalCars: ptr[int32](2),
	},
}

type rig struct {
	server natstest.Server
	kv     jetstream.KeyValue
	w      *Watcher
}

func start(t *testing.T, stale time.Duration) *rig {
	t.Helper()
	s := natstest.Bare(t)
	kv := natstest.Live(t, s)
	nc, err := nats.Connect(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	w := New(nc, stale, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return &rig{server: s, kv: kv, w: w}
}

func (r *rig) put(t *testing.T, key string, msg proto.Message) {
	t.Helper()
	data, err := protojson.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.kv.Put(t.Context(), key, data); err != nil {
		t.Fatal(err)
	}
}

// eventually waits up to 5 s for ok, which the watch makes true as values come.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("not %s after 5 s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func offline(s *pitwallv1.EngineStatus) bool {
	return proto.Equal(s, &pitwallv1.EngineStatus{Activity: pitwallv1.Activity_ACTIVITY_OFFLINE})
}

// AC1: the live values driving at Suzuka on lap 3 answer as v1's status route
// did (the chip's "Live · Suzuka Circuit East Course · L3").
func TestTheLiveValuesAnswer(t *testing.T) {
	r := start(t, 0)
	ecu, session, radio := driving()
	r.put(t, KeyEcuStatus, ecu)
	r.put(t, KeyEcuSession, session)
	r.put(t, KeyRadioStatus, radio)
	eventually(t, "driving", func() bool { return proto.Equal(r.w.Status(), want) })

	// The session ends: ecu deletes its key, and goes idle.
	if err := r.kv.Delete(t.Context(), KeyEcuSession); err != nil {
		t.Fatal(err)
	}
	ecu.Activity, ecu.IdleSecs = statev1.Activity_ACTIVITY_IDLE, 3
	r.put(t, KeyEcuStatus, ecu)
	eventually(t, "idle with no session", func() bool {
		s := r.w.Status()
		return s.GetActivity() == pitwallv1.Activity_ACTIVITY_IDLE && s.GetSession() == nil && s.GetIdleSecs() == 3
	})
}

// AC2: no value answers offline, and so does NATS gone, within 1 s.
func TestNoValueOrNoNATSIsOffline(t *testing.T) {
	r := start(t, 0)
	if s := r.w.Status(); !offline(s) {
		t.Errorf("no value: %v", s)
	}
	ecu, _, _ := driving()
	r.put(t, KeyEcuStatus, ecu)
	eventually(t, "driving", func() bool { return r.w.Status().GetActivity() == pitwallv1.Activity_ACTIVITY_DRIVING })
	r.server.Stop()
	start := time.Now()
	eventually(t, "offline", func() bool { return offline(r.w.Status()) })
	if d := time.Since(start); d > time.Second {
		t.Errorf("offline %v after NATS stopped, want within 1 s", d)
	}
}

// AC3: a value older than the stale limit is offline.
func TestAStaleValueIsOffline(t *testing.T) {
	r := start(t, 300*time.Millisecond)
	ecu, _, _ := driving()
	r.put(t, KeyEcuStatus, ecu)
	eventually(t, "driving", func() bool { return r.w.Status().GetActivity() == pitwallv1.Activity_ACTIVITY_DRIVING })
	eventually(t, "offline", func() bool { return offline(r.w.Status()) })
	// ecu writes again: it is back.
	r.put(t, KeyEcuStatus, ecu)
	eventually(t, "back", func() bool { return r.w.Status().GetActivity() == pitwallv1.Activity_ACTIVITY_DRIVING })
}

// A lap longer than the stale limit leaves ecu's session key unchanged, as ecu
// writes it only on a change: the session holds while ecu's status is fresh.
// radio's status, gone quiet, is judged on its own.
func TestALongLapKeepsItsSession(t *testing.T) {
	r := start(t, 300*time.Millisecond)
	ecu, session, radio := driving()
	r.put(t, KeyEcuSession, session)
	r.put(t, KeyRadioStatus, radio)
	r.put(t, KeyEcuStatus, ecu)
	eventually(t, "driving", func() bool { return proto.Equal(r.w.Status(), want) })
	// Well past the limit, with only ecu's heartbeat written.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		r.put(t, KeyEcuStatus, ecu)
		time.Sleep(50 * time.Millisecond)
	}
	eventually(t, "the heartbeat seen", func() bool { return r.w.Status().GetActivity() == pitwallv1.Activity_ACTIVITY_DRIVING })
	s := r.w.Status()
	if s.GetSession().GetSessionId() != session.GetSessionId() || s.GetSession().GetLap() != 3 {
		t.Errorf("a long lap lost its session: %v", s.GetSession())
	}
	if s.VoiceConnected != nil {
		t.Errorf("radio, silent past the limit, still says the voice: %v", s.GetVoiceConnected())
	}
}

// AC4: a replay's values leave the answer as it was.
func TestAReplayNeverShows(t *testing.T) {
	r := start(t, 0)
	ecu, session, _ := driving()
	ecu.Activity = statev1.Activity_ACTIVITY_IDLE
	r.put(t, KeyEcuStatus, ecu)
	eventually(t, "idle", func() bool { return r.w.Status().GetActivity() == pitwallv1.Activity_ACTIVITY_IDLE })
	replay, _, _ := driving()
	replay.Source = "replay-run-7"
	r.put(t, "ecu.status.replay-run-7", replay)
	r.put(t, "ecu.session.replay-run-7", session)
	// A marker written after the replay's values: once it is seen, they have
	// been delivered if they ever would be.
	ecu.IdleSecs = 9
	r.put(t, KeyEcuStatus, ecu)
	eventually(t, "the marker", func() bool { return r.w.Status().GetIdleSecs() == 9 })
	if s := r.w.Status(); s.GetActivity() != pitwallv1.Activity_ACTIVITY_IDLE || s.GetSession() != nil {
		t.Errorf("a replay shows: %v", s)
	}
}

// R6: no radio value, or one that says nothing of the voice, is no
// voice_connected ("Voice: unknown"), never false.
func TestVoiceIsUnknownWhenRadioSaysNothing(t *testing.T) {
	ecu, session, radio := driving()
	if s := Status(ecu, session, nil); s.VoiceConnected != nil {
		t.Errorf("no radio: voice %v", s.GetVoiceConnected())
	}
	radio.VoiceConnected = nil
	if s := Status(ecu, session, radio); s.VoiceConnected != nil {
		t.Errorf("radio silent on the voice: %v", s.GetVoiceConnected())
	}
	radio.VoiceConnected = ptr(false)
	if s := Status(ecu, session, radio); s.VoiceConnected == nil || s.GetVoiceConnected() {
		t.Errorf("voice off: %v", s.VoiceConnected)
	}
	// A session with no car, track or name yet.
	s := Status(ecu, &statev1.LiveSession{SessionId: "s", Track: &statev1.LiveTrack{}}, nil)
	if s.GetSession().Car != nil || s.GetSession().GetTrack().Name != nil || s.GetSession().GetKind() != "unknown" {
		t.Errorf("a new session: %v", s.GetSession())
	}
}

// A value that can't be read is dropped and logged, and the watch carries on;
// and the watch starts once the bucket is made.
func TestTheWatchWaitsForTheBucketAndSkipsBadValues(t *testing.T) {
	s := natstest.Bare(t)
	nc, err := nats.Connect(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	var logs syncBuffer
	w := New(nc, time.Minute, slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "a retry logged", func() bool { return bytes.Contains(logs.Bytes(), []byte("live watch")) })
	kv := natstest.Live(t, s)
	if _, err := kv.Put(t.Context(), KeyEcuStatus, []byte("{not json")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the bad value logged", func() bool { return bytes.Contains(logs.Bytes(), []byte("live value unreadable")) })
	if st := w.Status(); !offline(st) {
		t.Errorf("a bad value: %v", st)
	}
	ecu, _, _ := driving()
	data, _ := protojson.Marshal(ecu)
	if _, err := kv.Put(t.Context(), KeyEcuStatus, data); err != nil {
		t.Fatal(err)
	}
	eventually(t, "driving", func() bool { return w.Status().GetActivity() == pitwallv1.Activity_ACTIVITY_DRIVING })
	if New(nil, 0, nil).stale != DefaultStale {
		t.Error("the default stale limit is not DefaultStale")
	}
}

// syncBuffer is a log the watch writes while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}
