package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/google/go-cmp/cmp"

	pitwallv1 "github.com/those-who-run/contracts/gen/go/twr/pitwall/v1"
	"github.com/those-who-run/contracts/gen/go/twr/pitwall/v1/pitwallv1connect"
	storev1 "github.com/those-who-run/contracts/gen/go/twr/store/v1"

	"github.com/those-who-run/pitwall/internal/logbooktest"
	"github.com/those-who-run/pitwall/internal/natstest"
)

// The examples under testdata/contracts are contracts v0.2.7's
// examples/store/v1 and examples/pitwall/v1, copied unchanged: one session (the
// Suzuka East pit-stop race) as logbook answers it and as the API answers it.

var sources = []string{"live", "v1-import"}

func load(t *testing.T, name string, msg proto.Message) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "contracts", name+".json")) //nolint:gosec // the test's own fixtures
	if err != nil {
		t.Fatal(err)
	}
	if err := protojson.Unmarshal(data, msg); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

// rig is the API over a logbook stand-in on a real NATS server, served over HTTP.
type rig struct {
	logbook *logbooktest.Fake
	nc      *nats.Conn
	url     string
	client  pitwallv1connect.PitwallServiceClient
	server  *natstest.Server
}

func newRig(t *testing.T, timeout time.Duration) *rig {
	t.Helper()
	s := natstest.Bare(t)
	nc, err := nats.Connect(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	fake := logbooktest.Start(t, nc)
	server := connect.NewServer()
	New(nc, sources, "test", nil, timeout).Register(server)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server, connecthttp.WithCompressMinBytes(1024))
	hs := httptest.NewServer(mux)
	t.Cleanup(hs.Close)
	client := pitwallv1connect.NewPitwallServiceClient(connect.NewClient(
		connecthttp.NewTransport(hs.Client(), hs.URL, connecthttp.WithProtoJSON(), connecthttp.WithHTTPGet())))
	return &rig{logbook: fake, nc: nc, url: hs.URL, client: client, server: &s}
}

// ignoreCardText leaves out the card's words, which come from contracts' format
// once it has them (card.go's TODO).
var ignoreCardText = protocmp.IgnoreFields(&pitwallv1.Session{}, "title", "result", "kind_label")

func diff(want, got proto.Message, opts ...cmp.Option) string {
	return cmp.Diff(want, got, append([]cmp.Option{protocmp.Transform()}, opts...)...)
}

// request decodes the last request subject got.
func lastRequest(t *testing.T, r *rig, subject string, msg proto.Message) {
	t.Helper()
	reqs := r.logbook.Requests(subject)
	if len(reqs) == 0 {
		t.Fatalf("%s got no request", subject)
	}
	if err := protojson.Unmarshal(reqs[len(reqs)-1], msg); err != nil {
		t.Fatal(err)
	}
}

// AC1: each method maps logbook's answer to the API's, on one session.
func TestGetSessionMapsLogbooksAnswer(t *testing.T) {
	r := newRig(t, 0)
	var store storev1.GetSessionResponse
	load(t, "store/get_session_response", &store)
	r.logbook.Reply(logbooktest.SessionsGet, &store)
	got, err := r.client.GetSession(t.Context(), &pitwallv1.GetSessionRequest{SessionId: "01JB9QZ4V3X8M2K7T5R1N6PDWA"})
	if err != nil {
		t.Fatal(err)
	}
	var want pitwallv1.GetSessionResponse
	load(t, "pitwall/get_session_response", &want)
	if d := diff(&want, got, ignoreCardText); d != "" {
		t.Errorf("GetSession (-want +got):\n%s", d)
	}
	var sent storev1.GetSessionRequest
	lastRequest(t, r, logbooktest.SessionsGet, &sent)
	if d := diff(&storev1.GetSessionRequest{Sources: sources, SessionId: "01JB9QZ4V3X8M2K7T5R1N6PDWA"}, &sent); d != "" {
		t.Errorf("logbook's request (-want +got):\n%s", d)
	}
}

func TestListLapsMapsLogbooksAnswer(t *testing.T) {
	r := newRig(t, 0)
	var store storev1.ListLapsResponse
	load(t, "store/list_laps_response", &store)
	r.logbook.Reply(logbooktest.LapsList, &store)
	got, err := r.client.ListLaps(t.Context(), &pitwallv1.ListLapsRequest{SessionId: "01JB9QZ4V3X8M2K7T5R1N6PDWA"})
	if err != nil {
		t.Fatal(err)
	}
	var want pitwallv1.ListLapsResponse
	load(t, "pitwall/list_laps_response", &want)
	if d := diff(&want, got); d != "" {
		t.Errorf("ListLaps (-want +got):\n%s", d)
	}
}

func TestGetTraceMapsLogbooksAnswer(t *testing.T) {
	r := newRig(t, 0)
	var store storev1.GetTraceResponse
	load(t, "store/get_trace_response", &store)
	r.logbook.Reply(logbooktest.LapsTrace, &store)
	got, err := r.client.GetTrace(t.Context(), &pitwallv1.GetTraceRequest{SessionId: "01JB9QZ4V3X8M2K7T5R1N6PDWA", Lap: 5, Points: 4})
	if err != nil {
		t.Fatal(err)
	}
	var want pitwallv1.GetTraceResponse
	load(t, "pitwall/get_trace_response", &want)
	if d := diff(&want, got); d != "" {
		t.Errorf("GetTrace (-want +got):\n%s", d)
	}
	var sent storev1.GetTraceRequest
	lastRequest(t, r, logbooktest.LapsTrace, &sent)
	if sent.GetLap() != 5 || sent.GetPoints() != 4 {
		t.Errorf("logbook was asked for %v", &sent)
	}
}

// listAnswer is logbook's list with the example session, and the counts the
// API's example shows.
func listAnswer(t *testing.T) *storev1.ListSessionsResponse {
	t.Helper()
	var one storev1.GetSessionResponse
	load(t, "store/get_session_response", &one)
	return &storev1.ListSessionsResponse{
		Sessions: []*storev1.SessionRow{one.GetSession()},
		Options: &storev1.FilterCounts{
			Track: []*storev1.ValueCount{{Value: ""}, {Value: "Suzuka Circuit East Course", Count: 1}},
			Car:   []*storev1.ValueCount{{Value: "Honda Civic Si Group A version '87", Count: 1}},
			Kind: []*storev1.KindCount{
				{SessionType: 8}, {SessionType: 9}, {SessionType: 5, Count: 1},
			},
		},
		Total:       14,
		Matched:     1,
		DataVersion: "5:1829",
	}
}

func TestListSessionsMapsLogbooksAnswer(t *testing.T) {
	r := newRig(t, 0)
	r.logbook.Reply(logbooktest.SessionsList, listAnswer(t))
	got, err := r.client.ListSessions(t.Context(), &pitwallv1.ListSessionsRequest{
		Track: "Suzuka Circuit East Course", Kind: "lap_race", FromDay: "2026-10-01", TimeZone: "Europe/Dublin",
	})
	if err != nil {
		t.Fatal(err)
	}
	var want pitwallv1.ListSessionsResponse
	load(t, "pitwall/list_sessions_response", &want)
	if d := diff(&want, got, ignoreCardText); d != "" {
		t.Errorf("ListSessions (-want +got):\n%s", d)
	}
	// contracts' example request, but for the page limit, which is the pit
	// wall's, and the version, which a first GET has none of.
	var sent, wantSent storev1.ListSessionsRequest
	lastRequest(t, r, logbooktest.SessionsList, &sent)
	load(t, "store/list_sessions_request", &wantSent)
	wantSent.Limit, wantSent.KnownDataVersion = pageLimit, nil
	if d := diff(&wantSent, &sent); d != "" {
		t.Errorf("logbook's request (-want +got):\n%s", d)
	}
}

func TestListSessionsAsksForUnknownTrackAsTheEmptyName(t *testing.T) {
	r := newRig(t, 0)
	r.logbook.Reply(logbooktest.SessionsList, &storev1.ListSessionsResponse{DataVersion: "1"})
	if _, err := r.client.ListSessions(t.Context(), &pitwallv1.ListSessionsRequest{Track: UnknownTrack, Car: "Civic", Kind: "unknown"}); err != nil {
		t.Fatal(err)
	}
	var sent storev1.ListSessionsRequest
	lastRequest(t, r, logbooktest.SessionsList, &sent)
	if sent.Track == nil || sent.GetTrack() != "" || sent.GetCar() != "Civic" || sent.GetSessionType() != 4 {
		t.Errorf("logbook was asked for %v", &sent)
	}
}

func TestListSameTrackMapsLogbooksAnswer(t *testing.T) {
	r := newRig(t, 0)
	var one storev1.GetSessionResponse
	load(t, "store/get_session_response", &one)
	r.logbook.Reply(logbooktest.SameTrack, &storev1.ListSameTrackResponse{
		Sessions: []*storev1.SessionRow{one.GetSession()}, Matched: 1, DataVersion: "5:1829",
	})
	got, err := r.client.ListSameTrack(t.Context(), &pitwallv1.ListSameTrackRequest{SessionId: "other"})
	if err != nil {
		t.Fatal(err)
	}
	var want pitwallv1.GetSessionResponse
	load(t, "pitwall/get_session_response", &want)
	if len(got.GetSessions()) != 1 {
		t.Fatalf("got %d sessions, want 1", len(got.GetSessions()))
	}
	if d := diff(want.GetSession(), got.GetSessions()[0], ignoreCardText); d != "" {
		t.Errorf("ListSameTrack (-want +got):\n%s", d)
	}
}

// pages answers a list of n sessions two at a time, from the request's offset,
// with the version version(call) on each call.
func pages(n int, version func(call int) string) logbooktest.Answer {
	call := 0
	return func(data []byte) (proto.Message, error) {
		var req storev1.ListSessionsRequest
		if err := protojson.Unmarshal(data, &req); err != nil {
			return nil, err
		}
		call++
		resp := &storev1.ListSessionsResponse{Total: int32(n), Matched: int32(n), DataVersion: version(call)} //nolint:gosec // small
		for i := req.GetOffset(); i < req.GetOffset()+2 && i < int32(n); i++ {                                //nolint:gosec // small
			resp.Sessions = append(resp.Sessions, &storev1.SessionRow{SessionId: strconv.Itoa(int(i))})
		}
		return resp, nil
	}
}

func TestListSessionsReadsEveryPage(t *testing.T) {
	r := newRig(t, 0)
	r.logbook.Set(logbooktest.SessionsList, pages(5, func(int) string { return "v" }))
	got, err := r.client.ListSessions(t.Context(), &pitwallv1.ListSessionsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.GetSessions()) != 5 || got.GetMatched() != 5 || got.GetSessions()[4].GetSessionId() != "4" {
		t.Errorf("got %v", got)
	}
	if n := len(r.logbook.Requests(logbooktest.SessionsList)); n != 3 {
		t.Errorf("%d pages asked for, want 3", n)
	}
}

func TestAListThatChangesBetweenPagesStartsAgain(t *testing.T) {
	r := newRig(t, 0)
	// The second call's version differs from the first's: the list starts again,
	// and the pages from then on agree.
	r.logbook.Set(logbooktest.SessionsList, pages(3, func(call int) string {
		if call == 1 {
			return "old"
		}
		return "new"
	}))
	got, err := r.client.ListSessions(t.Context(), &pitwallv1.ListSessionsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.GetSessions()) != 3 {
		t.Errorf("got %d sessions, want 3", len(got.GetSessions()))
	}
	if n := len(r.logbook.Requests(logbooktest.SessionsList)); n != 4 {
		t.Errorf("%d pages asked for, want 4 (two, then two again)", n)
	}
}

func TestAListThatKeepsChangingIsAnsweredAfterItsTries(t *testing.T) {
	r := newRig(t, 0)
	r.logbook.Set(logbooktest.SessionsList, pages(3, strconv.Itoa))
	got, err := r.client.ListSessions(t.Context(), &pitwallv1.ListSessionsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(r.logbook.Requests(logbooktest.SessionsList)); n != 2*pageTries {
		t.Errorf("%d pages asked for, want %d", n, 2*pageTries)
	}
	if len(got.GetSessions()) != 2 {
		t.Errorf("got %d sessions, want the last try's 2", len(got.GetSessions()))
	}
}

// get is a Connect GET of procedure with req, with the headers given.
func get(t *testing.T, r *rig, procedure string, req proto.Message, headers map[string]string) *http.Response {
	t.Helper()
	msg, err := protojson.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	q := url.Values{"connect": {"v1"}, "encoding": {"json"}, "message": {string(msg)}}
	hr, err := http.NewRequestWithContext(t.Context(), http.MethodGet, r.url+procedure+"?"+q.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		hr.Header.Set(k, v)
	}
	// The transport's own gzip would hide Content-Encoding from the test.
	resp, err := (&http.Client{Transport: &http.Transport{DisableCompression: true}}).Do(hr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// AC2: a GET with the last ETag is a 304 with no body, logbook being asked with
// the version the ETag carries; after the data changes it is a 200 with a new
// ETag.
func TestAnETagAnswersNotModifiedUntilTheDataChanges(t *testing.T) {
	r := newRig(t, 0)
	version := "5:1829"
	r.logbook.Set(logbooktest.SessionsGet, func(data []byte) (proto.Message, error) {
		var req storev1.GetSessionRequest
		if err := protojson.Unmarshal(data, &req); err != nil {
			return nil, err
		}
		if req.GetKnownDataVersion() == version {
			return &storev1.GetSessionResponse{DataVersion: version, Unchanged: true}, nil
		}
		return &storev1.GetSessionResponse{Session: &storev1.SessionRow{SessionId: "s", Closed: true}, DataVersion: version}, nil
	})
	req := &pitwallv1.GetSessionRequest{SessionId: "s"}
	first := get(t, r, pitwallv1connect.PitwallServiceGetSessionProcedure, req, nil)
	tag := first.Header.Get("ETag")
	if first.StatusCode != http.StatusOK || !strings.HasPrefix(tag, `W/"`) || !strings.HasSuffix(tag, `:5:1829"`) {
		t.Fatalf("first GET: %d, ETag %q", first.StatusCode, tag)
	}
	if cc := first.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control %q, want no-cache", cc)
	}

	again := get(t, r, pitwallv1connect.PitwallServiceGetSessionProcedure, req, map[string]string{"If-None-Match": tag})
	body, _ := io.ReadAll(again.Body)
	if again.StatusCode != http.StatusNotModified || len(body) != 0 || again.Header.Get("ETag") != tag {
		t.Errorf("second GET: %d, %d bytes, ETag %q; want 304, none and %q", again.StatusCode, len(body), again.Header.Get("ETag"), tag)
	}
	var sent storev1.GetSessionRequest
	lastRequest(t, r, logbooktest.SessionsGet, &sent)
	if sent.GetKnownDataVersion() != "5:1829" {
		t.Errorf("logbook was sent known_data_version %q", sent.GetKnownDataVersion())
	}

	version = "5:1830" // a new lap
	after := get(t, r, pitwallv1connect.PitwallServiceGetSessionProcedure, req, map[string]string{"If-None-Match": tag})
	if newTag := after.Header.Get("ETag"); after.StatusCode != http.StatusOK || newTag == tag || !strings.HasSuffix(newTag, `:5:1830"`) {
		t.Errorf("after a new lap: %d, ETag %q (was %q)", after.StatusCode, newTag, tag)
	}

	// Another session's ETag is not this one's, so logbook is sent no version.
	other := get(t, r, pitwallv1connect.PitwallServiceGetSessionProcedure, &pitwallv1.GetSessionRequest{SessionId: "t"},
		map[string]string{"If-None-Match": tag})
	if other.StatusCode != http.StatusOK {
		t.Errorf("another session with this one's ETag: %d", other.StatusCode)
	}
	lastRequest(t, r, logbooktest.SessionsGet, &sent)
	if sent.KnownDataVersion != nil {
		t.Errorf("logbook was sent known_data_version %q for another session", sent.GetKnownDataVersion())
	}
}

// A logbook that answers in full though the version is the browser's still gives
// a 304.
func TestTheSameVersionIsNotModifiedWhateverLogbookSays(t *testing.T) {
	r := newRig(t, 0)
	r.logbook.Reply(logbooktest.LapsList, &storev1.ListLapsResponse{Session: &storev1.SessionRow{SessionId: "s"}, DataVersion: "7"})
	req := &pitwallv1.ListLapsRequest{SessionId: "s"}
	tag := get(t, r, pitwallv1connect.PitwallServiceListLapsProcedure, req, nil).Header.Get("ETag")
	again := get(t, r, pitwallv1connect.PitwallServiceListLapsProcedure, req, map[string]string{"If-None-Match": "W/\"x:1\", " + tag})
	if again.StatusCode != http.StatusNotModified {
		t.Errorf("GET with the ETag among others: %d, want 304", again.StatusCode)
	}
}

func TestEveryListAnswersNotModified(t *testing.T) {
	r := newRig(t, 0)
	unchanged := func(data []byte) (proto.Message, error) {
		if bytes.Contains(data, []byte(`"knownDataVersion":"9"`)) {
			return &storev1.ListSessionsResponse{DataVersion: "9", Unchanged: true}, nil
		}
		return &storev1.ListSessionsResponse{DataVersion: "9"}, nil
	}
	r.logbook.Set(logbooktest.SessionsList, unchanged)
	r.logbook.Set(logbooktest.SameTrack, func(data []byte) (proto.Message, error) {
		if bytes.Contains(data, []byte(`"knownDataVersion":"9"`)) {
			return &storev1.ListSameTrackResponse{DataVersion: "9", Unchanged: true}, nil
		}
		return &storev1.ListSameTrackResponse{DataVersion: "9"}, nil
	})
	r.logbook.Set(logbooktest.LapsTrace, func(data []byte) (proto.Message, error) {
		if bytes.Contains(data, []byte(`"knownDataVersion":"9"`)) {
			return &storev1.GetTraceResponse{DataVersion: "9", Unchanged: true}, nil
		}
		return &storev1.GetTraceResponse{Trace: &storev1.LapTrace{SessionId: "s", Lap: 1}, DataVersion: "9"}, nil
	})
	for _, c := range []struct {
		procedure string
		req       proto.Message
	}{
		{pitwallv1connect.PitwallServiceListSessionsProcedure, &pitwallv1.ListSessionsRequest{}},
		{pitwallv1connect.PitwallServiceListSameTrackProcedure, &pitwallv1.ListSameTrackRequest{SessionId: "s"}},
		{pitwallv1connect.PitwallServiceGetTraceProcedure, &pitwallv1.GetTraceRequest{SessionId: "s", Lap: 1}},
	} {
		tag := get(t, r, c.procedure, c.req, nil).Header.Get("ETag")
		if resp := get(t, r, c.procedure, c.req, map[string]string{"If-None-Match": tag}); resp.StatusCode != http.StatusNotModified {
			t.Errorf("%s with its ETag %q: %d, want 304", c.procedure, tag, resp.StatusCode)
		}
	}
}

// AC5: a trace over 1 KB is gzipped when the browser takes it, and a small
// answer is not.
func TestABigAnswerIsGzipped(t *testing.T) {
	r := newRig(t, 0)
	trace := &storev1.LapTrace{SessionId: "s", Lap: 1}
	for i := range 300 {
		trace.T = append(trace.T, float64(i)/60)
		trace.SpeedKph = append(trace.SpeedKph, 100+float64(i%50))
	}
	r.logbook.Reply(logbooktest.LapsTrace, &storev1.GetTraceResponse{Trace: trace, DataVersion: "1"})
	r.logbook.Reply(logbooktest.SessionsGet, &storev1.GetSessionResponse{Session: &storev1.SessionRow{SessionId: "s"}, DataVersion: "1"})
	resp := get(t, r, pitwallv1connect.PitwallServiceGetTraceProcedure, &pitwallv1.GetTraceRequest{SessionId: "s", Lap: 1},
		map[string]string{"Accept-Encoding": "gzip"})
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding %q, want gzip", resp.Header.Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var got pitwallv1.GetTraceResponse
	data, _ := io.ReadAll(zr)
	if err := protojson.Unmarshal(data, &got); err != nil || len(got.GetTrace().GetT()) != 300 {
		t.Errorf("the gzipped trace: %v, %d points", err, len(got.GetTrace().GetT()))
	}
	small := get(t, r, pitwallv1connect.PitwallServiceGetSessionProcedure, &pitwallv1.GetSessionRequest{SessionId: "s"},
		map[string]string{"Accept-Encoding": "gzip"})
	if small.Header.Get("Content-Encoding") == "gzip" {
		t.Error("an answer under 1 KB is gzipped")
	}
}

func code(err error) connect.Code { return connect.CodeOf(err) }

// R3: logbook's 404 is not_found and its 400 invalid_argument.
func TestLogbooksErrorsKeepTheirMeaning(t *testing.T) {
	r := newRig(t, 0)
	for _, c := range []struct {
		code string
		want connect.Code
	}{{"404", connect.CodeNotFound}, {"400", connect.CodeInvalidArgument}, {"500", connect.CodeInternal}, {"503", connect.CodeUnavailable}} {
		r.logbook.Set(logbooktest.SessionsGet, func([]byte) (proto.Message, error) {
			return nil, &logbooktest.Error{Code: c.code, Text: "no such session"}
		})
		_, err := r.client.GetSession(t.Context(), &pitwallv1.GetSessionRequest{SessionId: "nope"})
		if code(err) != c.want {
			t.Errorf("logbook's %s: %v, want %v", c.code, err, c.want)
		}
	}
}

func TestAnAnswerThatIsNotJSONIsInternal(t *testing.T) {
	s := natstest.Bare(t)
	nc, err := nats.Connect(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	if _, err := nc.Subscribe("svc.logbook.laps.list", func(m *nats.Msg) { _ = m.Respond([]byte("{not json")) }); err != nil {
		t.Fatal(err)
	}
	_, err = New(nc, sources, "test", nil, 0).ListLaps(t.Context(), &pitwallv1.ListLapsRequest{SessionId: "s"})
	if code(err) != connect.CodeInternal {
		t.Errorf("a reply that is not JSON: %v, want internal", err)
	}
}

// AC4: logbook gone is unavailable within 2.5 s, whether nothing answers or it
// answers too late.
func TestLogbookNotAnsweringIsUnavailable(t *testing.T) {
	s := natstest.Bare(t)
	nc, err := nats.Connect(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	srv := New(nc, sources, "test", nil, 0)
	start := time.Now()
	_, err = srv.GetSession(t.Context(), &pitwallv1.GetSessionRequest{SessionId: "s"})
	if code(err) != connect.CodeUnavailable || time.Since(start) > 2500*time.Millisecond {
		t.Errorf("no logbook: %v after %v, want unavailable within 2.5 s", err, time.Since(start))
	}

	// A logbook that never answers: the timeout, shortened for the test.
	if _, err := nc.Subscribe("svc.logbook.sessions.get", func(*nats.Msg) {}); err != nil {
		t.Fatal(err)
	}
	slow := New(nc, sources, "test", nil, 100*time.Millisecond)
	if _, err := slow.GetSession(t.Context(), &pitwallv1.GetSessionRequest{SessionId: "s"}); code(err) != connect.CodeUnavailable {
		t.Errorf("a silent logbook: %v, want unavailable", err)
	}
	if New(nc, sources, "test", nil, 0).store.timeout != 2*time.Second {
		t.Error("the default timeout is not 2 s")
	}
}

func TestACancelledCallIsCancelled(t *testing.T) {
	s := natstest.Bare(t)
	nc, err := nats.Connect(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	if _, err := nc.Subscribe("svc.logbook.sessions.get", func(*nats.Msg) {}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(50*time.Millisecond, cancel)
	_, err = New(nc, sources, "test", nil, 0).GetSession(ctx, &pitwallv1.GetSessionRequest{SessionId: "s"})
	if code(err) != connect.CodeCanceled {
		t.Errorf("a cancelled call: %v, want canceled", err)
	}
}

func TestBadArgumentsAreInvalid(t *testing.T) {
	r := newRig(t, 0)
	ctx := t.Context()
	for name, call := range map[string]func() error{
		"kind": func() error {
			_, err := r.client.ListSessions(ctx, &pitwallv1.ListSessionsRequest{Kind: "LAP_RACE"})
			return err
		},
		"no kind": func() error {
			_, err := r.client.ListSessions(ctx, &pitwallv1.ListSessionsRequest{Kind: "sprint"})
			return err
		},
		"from_day": func() error {
			_, err := r.client.ListSessions(ctx, &pitwallv1.ListSessionsRequest{FromDay: "17/10/2026"})
			return err
		},
		"to_day": func() error {
			_, err := r.client.ListSessions(ctx, &pitwallv1.ListSessionsRequest{ToDay: "2026-13-01"})
			return err
		},
		"time_zone": func() error {
			_, err := r.client.ListSessions(ctx, &pitwallv1.ListSessionsRequest{TimeZone: "Mars/Olympus"})
			return err
		},
		"session":    func() error { _, err := r.client.GetSession(ctx, &pitwallv1.GetSessionRequest{}); return err },
		"laps":       func() error { _, err := r.client.ListLaps(ctx, &pitwallv1.ListLapsRequest{}); return err },
		"same track": func() error { _, err := r.client.ListSameTrack(ctx, &pitwallv1.ListSameTrackRequest{}); return err },
		"trace":      func() error { _, err := r.client.GetTrace(ctx, &pitwallv1.GetTraceRequest{Lap: 1}); return err },
		"lap":        func() error { _, err := r.client.GetTrace(ctx, &pitwallv1.GetTraceRequest{SessionId: "s"}); return err },
		"points": func() error {
			_, err := r.client.GetTrace(ctx, &pitwallv1.GetTraceRequest{SessionId: "s", Lap: 1, Points: 10001})
			return err
		},
	} {
		if err := call(); code(err) != connect.CodeInvalidArgument {
			t.Errorf("a bad %s: %v, want invalid_argument", name, err)
		}
	}
	if n := len(r.logbook.Requests(logbooktest.SessionsList)) + len(r.logbook.Requests(logbooktest.LapsTrace)); n != 0 {
		t.Errorf("logbook was asked %d times for bad arguments", n)
	}
}

func TestEveryErrorReachesTheCaller(t *testing.T) {
	r := newRig(t, 0)
	gone := func([]byte) (proto.Message, error) { return nil, &logbooktest.Error{Code: "404", Text: "gone"} }
	for _, s := range []string{logbooktest.SessionsList, logbooktest.SameTrack, logbooktest.LapsList, logbooktest.LapsTrace} {
		r.logbook.Set(s, gone)
	}
	ctx := t.Context()
	for name, call := range map[string]func() error{
		"list": func() error { _, err := r.client.ListSessions(ctx, &pitwallv1.ListSessionsRequest{}); return err },
		"same track": func() error {
			_, err := r.client.ListSameTrack(ctx, &pitwallv1.ListSameTrackRequest{SessionId: "s"})
			return err
		},
		"laps": func() error { _, err := r.client.ListLaps(ctx, &pitwallv1.ListLapsRequest{SessionId: "s"}); return err },
		"trace": func() error {
			_, err := r.client.GetTrace(ctx, &pitwallv1.GetTraceRequest{SessionId: "s", Lap: 1})
			return err
		},
	} {
		if err := call(); code(err) != connect.CodeNotFound {
			t.Errorf("%s: %v, want not_found", name, err)
		}
	}
	// A list whose second page fails fails.
	r.logbook.Set(logbooktest.SessionsList, func(data []byte) (proto.Message, error) {
		if bytes.Contains(data, []byte(`"offset"`)) {
			return nil, errors.New("disk on fire")
		}
		return &storev1.ListSessionsResponse{Sessions: []*storev1.SessionRow{{SessionId: "a"}}, Matched: 2, DataVersion: "1"}, nil
	})
	if _, err := r.client.ListSessions(ctx, &pitwallv1.ListSessionsRequest{}); code(err) != connect.CodeInternal {
		t.Errorf("a failing second page: %v, want internal", err)
	}
}

// fixedEngine is an Engine with a set status.
type fixedEngine struct{ status *pitwallv1.EngineStatus }

func (e fixedEngine) Status() *pitwallv1.EngineStatus { return e.status }

// paddock#125 R5: the status is never cached, and with no engine it is offline.
func TestEngineStatusIsNeverCached(t *testing.T) {
	r := newRig(t, 0)
	resp := get(t, r, pitwallv1connect.PitwallServiceGetEngineStatusProcedure, &pitwallv1.GetEngineStatusRequest{}, nil)
	body, _ := io.ReadAll(resp.Body)
	if resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("ETag") != "" {
		t.Errorf("Cache-Control %q, ETag %q; want no-store and none", resp.Header.Get("Cache-Control"), resp.Header.Get("ETag"))
	}
	var want pitwallv1.GetEngineStatusResponse
	load(t, "pitwall/get_engine_status_offline", &want)
	var got pitwallv1.GetEngineStatusResponse
	if err := protojson.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if d := diff(&want, &got); d != "" {
		t.Errorf("no engine (-want +got):\n%s", d)
	}

	driving := &pitwallv1.EngineStatus{Activity: pitwallv1.Activity_ACTIVITY_DRIVING}
	srv := New(r.nc, sources, "test", fixedEngine{driving}, 0)
	got2, err := srv.GetEngineStatus(t.Context(), &pitwallv1.GetEngineStatusRequest{})
	if err != nil || got2.GetStatus() != driving {
		t.Errorf("GetEngineStatus = %v, %v; want the engine's", got2, err)
	}
}

func TestAnETagHoldsOnlyAVersionItCanCarry(t *testing.T) {
	v := &view{key: "k"}
	for _, bad := range []string{"", `a"b`, "a b", "é"} {
		if tag := v.etag(bad); tag != "" {
			t.Errorf("etag(%q) = %q, want none", bad, tag)
		}
	}
	if err := v.answer("1", true); err != nil {
		t.Errorf("an answer with no call info = %v, want nil", err)
	}
	if got := knownVersion(`W/"k:", "j:1", W/"k:5:1"`, "k"); got == nil || *got != "5:1" {
		t.Errorf("knownVersion = %v, want 5:1", got)
	}
}
