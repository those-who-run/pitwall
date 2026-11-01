package api

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pitwallv1 "github.com/those-who-run/contracts/gen/go/twr/pitwall/v1"
	"github.com/those-who-run/contracts/gen/go/twr/pitwall/v1/pitwallv1connect"
	storev1 "github.com/those-who-run/contracts/gen/go/twr/store/v1"

	"github.com/those-who-run/pitwall/internal/logbooktest"
	"github.com/those-who-run/pitwall/internal/natstest"
)

// ADR 0014: DeleteSession asks logbook's svc.logbook.sessions.delete for the
// pit wall's sources and maps the session it answers, deleted_at and all.
func TestDeleteSessionMapsLogbooksAnswer(t *testing.T) {
	r := newRig(t, 0)
	var store storev1.DeleteSessionResponse
	load(t, "store/delete_session_response", &store)
	r.logbook.Reply(logbooktest.SessionsDelete, &store)
	got, err := r.client.DeleteSession(t.Context(), &pitwallv1.DeleteSessionRequest{SessionId: "01JB9QZ4V3X8M2K7T5R1N6PDWA"})
	if err != nil {
		t.Fatal(err)
	}
	var want pitwallv1.DeleteSessionResponse
	load(t, "pitwall/delete_session_response", &want)
	if d := diff(&want, got); d != "" {
		t.Errorf("DeleteSession (-want +got):\n%s", d)
	}
	var sent, wantSent storev1.DeleteSessionRequest
	lastRequest(t, r, logbooktest.SessionsDelete, &sent)
	load(t, "store/delete_session_request", &wantSent)
	if d := diff(&wantSent, &sent); d != "" {
		t.Errorf("logbook's request (-want +got):\n%s", d)
	}
}

func TestRestoreSessionMapsLogbooksAnswer(t *testing.T) {
	r := newRig(t, 0)
	var store storev1.RestoreSessionResponse
	load(t, "store/restore_session_response", &store)
	r.logbook.Reply(logbooktest.SessionsRestore, &store)
	got, err := r.client.RestoreSession(t.Context(), &pitwallv1.RestoreSessionRequest{SessionId: "01JB9QZ4V3X8M2K7T5R1N6PDWA"})
	if err != nil {
		t.Fatal(err)
	}
	var want pitwallv1.RestoreSessionResponse
	load(t, "pitwall/restore_session_response", &want)
	if d := diff(&want, got); d != "" {
		t.Errorf("RestoreSession (-want +got):\n%s", d)
	}
	var sent storev1.RestoreSessionRequest
	lastRequest(t, r, logbooktest.SessionsRestore, &sent)
	if d := diff(&storev1.RestoreSessionRequest{Sources: sources, SessionId: "01JB9QZ4V3X8M2K7T5R1N6PDWA"}, &sent); d != "" {
		t.Errorf("logbook's request (-want +got):\n%s", d)
	}
}

// A deleted session's GetSession carries deleted_at, so its page can offer
// Restore.
func TestADeletedSessionsPageCarriesDeletedAt(t *testing.T) {
	r := newRig(t, 0)
	at := timestamppb.New(time.Date(2026, 11, 1, 19, 42, 7, 0, time.UTC))
	r.logbook.Reply(logbooktest.SessionsGet, &storev1.GetSessionResponse{
		Session: &storev1.SessionRow{SessionId: "s", Closed: true, DeletedAt: at}, DataVersion: "1",
	})
	got, err := r.client.GetSession(t.Context(), &pitwallv1.GetSessionRequest{SessionId: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(got.GetSession().GetDeletedAt(), at) {
		t.Errorf("deleted_at = %v, want %v", got.GetSession().GetDeletedAt(), at)
	}
}

// logbook's 404 is not_found, its 409 (an open session) failed_precondition,
// and an empty id invalid_argument without asking logbook.
func TestDeleteAndRestoreErrorsKeepTheirMeaning(t *testing.T) {
	r := newRig(t, 0)
	ctx := t.Context()
	calls := map[string]func(id string) error{
		"delete": func(id string) error {
			_, err := r.client.DeleteSession(ctx, &pitwallv1.DeleteSessionRequest{SessionId: id})
			return err
		},
		"restore": func(id string) error {
			_, err := r.client.RestoreSession(ctx, &pitwallv1.RestoreSessionRequest{SessionId: id})
			return err
		},
	}
	for _, c := range []struct {
		code string
		want connect.Code
	}{{"404", connect.CodeNotFound}, {"409", connect.CodeFailedPrecondition}, {"400", connect.CodeInvalidArgument}, {"500", connect.CodeInternal}} {
		answer := func([]byte) (proto.Message, error) { return nil, &logbooktest.Error{Code: c.code, Text: "no"} }
		r.logbook.Set(logbooktest.SessionsDelete, answer)
		r.logbook.Set(logbooktest.SessionsRestore, answer)
		for name, call := range calls {
			if err := call("s"); code(err) != c.want {
				t.Errorf("%s, logbook's %s: %v, want %v", name, c.code, err, c.want)
			}
		}
	}
	before := len(r.logbook.Requests(logbooktest.SessionsDelete)) + len(r.logbook.Requests(logbooktest.SessionsRestore))
	for name, call := range calls {
		if err := call(""); code(err) != connect.CodeInvalidArgument {
			t.Errorf("%s with no session: %v, want invalid_argument", name, err)
		}
	}
	if n := len(r.logbook.Requests(logbooktest.SessionsDelete)) + len(r.logbook.Requests(logbooktest.SessionsRestore)); n != before {
		t.Errorf("logbook was asked %d times for no session", n-before)
	}
}

// No logbook answering a delete or a restore is unavailable.
func TestADeleteWithNoLogbookIsUnavailable(t *testing.T) {
	s := natstest.Bare(t)
	nc, err := nats.Connect(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	srv := New(nc, sources, "test", nil, 100*time.Millisecond)
	if _, err := srv.DeleteSession(t.Context(), &pitwallv1.DeleteSessionRequest{SessionId: "s"}); code(err) != connect.CodeUnavailable {
		t.Errorf("delete with no logbook: %v, want unavailable", err)
	}
	if _, err := srv.RestoreSession(t.Context(), &pitwallv1.RestoreSessionRequest{SessionId: "s"}); code(err) != connect.CodeUnavailable {
		t.Errorf("restore with no logbook: %v, want unavailable", err)
	}
}

// post is a Connect POST of procedure with body, in JSON.
func post(t *testing.T, r *rig, procedure, body string) *http.Response {
	t.Helper()
	hr, err := http.NewRequestWithContext(t.Context(), http.MethodPost, r.url+procedure, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	hr.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(hr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func readAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The writes are never cached: no ETag, and no-store, on an answer and on an
// error.
func TestTheWritesAreNeverCached(t *testing.T) {
	r := newRig(t, 0)
	r.logbook.Reply(logbooktest.SessionsDelete, &storev1.DeleteSessionResponse{Session: &storev1.SessionRow{SessionId: "s"}, DataVersion: "2"})
	r.logbook.Set(logbooktest.SessionsRestore, func([]byte) (proto.Message, error) {
		return nil, &logbooktest.Error{Code: "404", Text: "no such session"}
	})
	for _, c := range []struct {
		procedure string
		status    int
	}{
		{pitwallv1connect.PitwallServiceDeleteSessionProcedure, http.StatusOK},
		{pitwallv1connect.PitwallServiceRestoreSessionProcedure, http.StatusNotFound},
	} {
		resp := post(t, r, c.procedure, `{"sessionId":"s"}`)
		if resp.StatusCode != c.status || resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("ETag") != "" {
			t.Errorf("%s: %d, Cache-Control %q, ETag %q; want %d, no-store and none",
				c.procedure, resp.StatusCode, resp.Header.Get("Cache-Control"), resp.Header.Get("ETag"), c.status)
		}
	}
}

// fakeStore is a logbook with one session whose delete and restore change the
// store's data_version, as logbook's do (a repeat keeps it), and whose list
// leaves the session out while it is deleted.
type fakeStore struct {
	mu      sync.Mutex
	version int
	deleted bool
}

func (f *fakeStore) dataVersion() string { return "5:" + strconv.Itoa(1829+f.version) }

func (f *fakeStore) list(data []byte) (proto.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Contains(string(data), `"knownDataVersion":"`+f.dataVersion()+`"`) {
		return &storev1.ListSessionsResponse{DataVersion: f.dataVersion(), Unchanged: true}, nil
	}
	resp := &storev1.ListSessionsResponse{DataVersion: f.dataVersion()}
	if !f.deleted {
		resp.Sessions, resp.Total, resp.Matched = []*storev1.SessionRow{{SessionId: "s", Closed: true}}, 1, 1
	}
	return resp, nil
}

func (f *fakeStore) set(deleted bool) logbooktest.Answer {
	return func([]byte) (proto.Message, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.deleted != deleted {
			f.deleted = deleted
			f.version++
		}
		row := &storev1.SessionRow{SessionId: "s", Closed: true}
		if deleted {
			row.DeletedAt = timestamppb.Now()
			return &storev1.DeleteSessionResponse{Session: row, DataVersion: f.dataVersion()}, nil
		}
		return &storev1.RestoreSessionResponse{Session: row, DataVersion: f.dataVersion()}, nil
	}
}

// ADR 0014: after a delete or a restore the list's ETag changes, so a browser
// holding the old one gets the new list, not a 304; a repeat changes nothing.
func TestTheListsETagChangesAfterADeleteOrARestore(t *testing.T) {
	r := newRig(t, 0)
	f := &fakeStore{}
	r.logbook.Set(logbooktest.SessionsList, f.list)
	r.logbook.Set(logbooktest.SessionsDelete, f.set(true))
	r.logbook.Set(logbooktest.SessionsRestore, f.set(false))
	list := pitwallv1connect.PitwallServiceListSessionsProcedure
	req := &pitwallv1.ListSessionsRequest{}
	sessions := func(resp *http.Response) int {
		t.Helper()
		var body pitwallv1.ListSessionsResponse
		data := readAll(t, resp)
		if err := unmarshal.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		return len(body.GetSessions())
	}

	first := get(t, r, list, req, nil)
	tag := first.Header.Get("ETag")
	if n := sessions(first); n != 1 || tag == "" {
		t.Fatalf("the list before: %d sessions, ETag %q", n, tag)
	}
	if again := get(t, r, list, req, map[string]string{"If-None-Match": tag}); again.StatusCode != http.StatusNotModified {
		t.Fatalf("the list again: %d, want 304", again.StatusCode)
	}

	for _, step := range []struct {
		name     string
		write    func() error
		sessions int
	}{
		{"delete", func() error {
			_, err := r.client.DeleteSession(t.Context(), &pitwallv1.DeleteSessionRequest{SessionId: "s"})
			return err
		}, 0},
		{"restore", func() error {
			_, err := r.client.RestoreSession(t.Context(), &pitwallv1.RestoreSessionRequest{SessionId: "s"})
			return err
		}, 1},
	} {
		if err := step.write(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		after := get(t, r, list, req, map[string]string{"If-None-Match": tag})
		newTag := after.Header.Get("ETag")
		if after.StatusCode != http.StatusOK || newTag == "" || newTag == tag {
			t.Fatalf("the list after a %s: %d, ETag %q (was %q); want 200 and a new ETag", step.name, after.StatusCode, newTag, tag)
		}
		if n := sessions(after); n != step.sessions {
			t.Errorf("the list after a %s has %d sessions, want %d", step.name, n, step.sessions)
		}
		// A repeat changes nothing, so the new ETag holds.
		if err := step.write(); err != nil {
			t.Fatalf("%s again: %v", step.name, err)
		}
		if same := get(t, r, list, req, map[string]string{"If-None-Match": newTag}); same.StatusCode != http.StatusNotModified {
			t.Errorf("the list after a second %s: %d, want 304", step.name, same.StatusCode)
		}
		tag = newTag
	}
}
