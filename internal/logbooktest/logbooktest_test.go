package logbooktest

import (
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	storev1 "github.com/those-who-run/contracts/gen/go/twr/store/v1"

	"github.com/those-who-run/pitwall/internal/natstest"
)

// The stand-in answers as logbook does: the message set, NATS micro's error
// headers for an error, and 500 for an endpoint with nothing set.
func TestTheFakeAnswersAsLogbook(t *testing.T) {
	s := natstest.Bare(t)
	nc, err := nats.Connect(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	f := Start(t, nc)
	ask := func(subject string) *nats.Msg {
		t.Helper()
		m, err := nc.Request(subject, []byte(`{"sources":["live"]}`), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	if m := ask(SessionsGet); m.Header.Get("Nats-Service-Error-Code") != "500" {
		t.Errorf("nothing set: %v", m.Header)
	}
	f.Reply(SessionsGet, &storev1.GetSessionResponse{DataVersion: "1"})
	if m := ask(SessionsGet); string(m.Data) != `{"dataVersion":"1"}` {
		t.Errorf("a reply: %s", m.Data)
	}
	f.Set(LapsList, func([]byte) (proto.Message, error) { return nil, &Error{Code: "404", Text: "no such session"} })
	if m := ask(LapsList); m.Header.Get("Nats-Service-Error-Code") != "404" || m.Header.Get("Nats-Service-Error") != "no such session" {
		t.Errorf("an error: %v", m.Header)
	}
	f.Set(LapsTrace, func([]byte) (proto.Message, error) { return nil, errors.New("disk on fire") })
	if m := ask(LapsTrace); m.Header.Get("Nats-Service-Error-Code") != "500" {
		t.Errorf("a plain error: %v", m.Header)
	}
	f.Reply(SameTrack, &storev1.ListSameTrackResponse{DataVersion: "\xff"}) // not UTF-8: no JSON
	if m := ask(SameTrack); m.Header.Get("Nats-Service-Error-Code") != "500" {
		t.Errorf("a message with no JSON: %v %s", m.Header, m.Data)
	}
	if got := f.Requests(SessionsGet); len(got) != 2 || string(got[0]) != `{"sources":["live"]}` {
		t.Errorf("requests: %q", got)
	}
	if (&Error{Code: "400", Text: "bad"}).Error() != "400 bad" {
		t.Error("Error()")
	}
}
