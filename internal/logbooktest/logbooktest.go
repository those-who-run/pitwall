// Package logbooktest is a stand-in for logbook's micro service
// (svc.logbook.*, paddock#122 and #123) on a real NATS server, for the pit wall's
// tests: each endpoint answers what the test sets, in protobuf's JSON form, or
// NATS micro's error headers, as logbook does. Only tests import it.
package logbooktest

import (
	"sync"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// The endpoints' subjects, as contracts' subjects.yaml lists them.
const (
	SessionsList = "svc.logbook.sessions.list"
	SessionsGet  = "svc.logbook.sessions.get"
	SameTrack    = "svc.logbook.sessions.same_track"
	LapsList     = "svc.logbook.laps.list"
	LapsTrace    = "svc.logbook.laps.trace"
	// The writes (ADR 0014 in paddock).
	SessionsDelete  = "svc.logbook.sessions.delete"
	SessionsRestore = "svc.logbook.sessions.restore"
)

var endpoints = map[string]string{
	"sessions_list":    SessionsList,
	"sessions_get":     SessionsGet,
	"same_track":       SameTrack,
	"laps_list":        LapsList,
	"laps_trace":       LapsTrace,
	"sessions_delete":  SessionsDelete,
	"sessions_restore": SessionsRestore,
}

// Error is an error reply: NATS micro's Nats-Service-Error-Code and
// Nats-Service-Error.
type Error struct {
	Code string
	Text string
}

func (e *Error) Error() string { return e.Code + " " + e.Text }

// Answer is an endpoint's reply to a request: a message, or an *Error.
type Answer func(request []byte) (proto.Message, error)

// Fake is the stand-in.
type Fake struct {
	mu       sync.Mutex
	answers  map[string]Answer
	requests map[string][][]byte
}

// Start adds the logbook micro service on nc, stopped when the test ends. An
// endpoint with no answer set answers 500.
func Start(t testing.TB, nc *nats.Conn) *Fake {
	t.Helper()
	f := &Fake{answers: map[string]Answer{}, requests: map[string][][]byte{}}
	svc, err := micro.AddService(nc, micro.Config{Name: "logbook", Version: "0.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Stop() })
	for name, subject := range endpoints {
		err := svc.AddEndpoint(name, micro.HandlerFunc(func(r micro.Request) { f.serve(subject, r) }),
			micro.WithEndpointSubject(subject))
		if err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// Set sets subject's answer.
func (f *Fake) Set(subject string, a Answer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[subject] = a
}

// Reply sets subject's answer to msg, whatever the request.
func (f *Fake) Reply(subject string, msg proto.Message) {
	f.Set(subject, func([]byte) (proto.Message, error) { return msg, nil })
}

// Requests are the requests subject got, in order.
func (f *Fake) Requests(subject string) [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.requests[subject]...)
}

func (f *Fake) serve(subject string, r micro.Request) {
	f.mu.Lock()
	f.requests[subject] = append(f.requests[subject], append([]byte(nil), r.Data()...))
	a := f.answers[subject]
	f.mu.Unlock()
	if a == nil {
		_ = r.Error("500", "no answer set", nil)
		return
	}
	msg, err := a(r.Data())
	if e, ok := err.(*Error); ok { //nolint:errorlint // the test's own type, never wrapped
		_ = r.Error(e.Code, e.Text, nil)
		return
	}
	if err != nil {
		_ = r.Error("500", err.Error(), nil)
		return
	}
	data, err := protojson.Marshal(msg)
	if err != nil {
		_ = r.Error("500", err.Error(), nil)
		return
	}
	_ = r.Respond(data)
}
