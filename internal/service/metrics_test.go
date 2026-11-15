package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pitwallv1 "github.com/those-who-run/contracts/gen/go/twr/pitwall/v1"
	"github.com/those-who-run/contracts/gen/go/twr/pitwall/v1/pitwallv1connect"
	storev1 "github.com/those-who-run/contracts/gen/go/twr/store/v1"

	"github.com/those-who-run/pitwall/internal/api"
	"github.com/those-who-run/pitwall/internal/logbooktest"
	"github.com/those-who-run/pitwall/internal/metrics"
	"github.com/those-who-run/pitwall/internal/natstest"
)

// metricsRig is the whole handler, with the web app, over a logbook stand-in on a
// real NATS server (none when withLogbook is false), served over HTTP.
type metricsRig struct {
	logbook *logbooktest.Fake
	url     string
}

func newMetricsRig(t *testing.T, withLogbook bool) *metricsRig {
	t.Helper()
	s := natstest.Bare(t)
	nc, err := nats.Connect(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	r := &metricsRig{}
	if withLogbook {
		r.logbook = logbooktest.Start(t, nc)
	}
	m := metrics.New()
	h := testHandler(t, Deps{
		API:       api.New(nc, DefaultSources, "test", nil, 200*time.Millisecond, api.WithMetrics(m)),
		StaticDir: webApp(t),
		Metrics:   m,
	})
	hs := httptest.NewServer(h)
	t.Cleanup(hs.Close)
	r.url = hs.URL
	return r
}

// call is a Connect GET of procedure with req, with the headers; it returns the
// status and the body.
func (r *metricsRig) call(t *testing.T, procedure string, req proto.Message, headers map[string]string) (int, http.Header, []byte) {
	t.Helper()
	msg, err := protojson.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	q := url.Values{"connect": {"v1"}, "encoding": {"json"}, "message": {string(msg)}}
	return r.get(t, procedure+"?"+q.Encode(), headers)
}

func (r *metricsRig) get(t *testing.T, target string, headers map[string]string) (int, http.Header, []byte) {
	t.Helper()
	hr, err := http.NewRequestWithContext(t.Context(), http.MethodGet, r.url+target, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		hr.Header.Set(k, v)
	}
	resp, err := (&http.Client{Transport: &http.Transport{DisableCompression: true}}).Do(hr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, body
}

// scrape is GET /metrics.
func (r *metricsRig) scrape(t *testing.T) string {
	t.Helper()
	status, hdr, body := r.get(t, "/metrics", nil)
	if status != http.StatusOK || !strings.HasPrefix(hdr.Get("Content-Type"), "text/plain") {
		t.Fatalf("GET /metrics: %d, %s", status, hdr.Get("Content-Type"))
	}
	return string(body)
}

// value is the value of the series line that starts with series, which is its
// name and labels as the text format writes them (labels in name order); -1 when
// there is no such line.
func value(body, series string) string {
	for line := range strings.SplitSeq(body, "\n") {
		if rest, ok := strings.CutPrefix(line, series+" "); ok {
			return rest
		}
	}
	return "-1"
}

func expect(t *testing.T, body, series, want string) {
	t.Helper()
	if got := value(body, series); got != want {
		t.Errorf("%s = %s, want %s", series, got, want)
	}
}

const (
	getSession = pitwallv1connect.PitwallServiceGetSessionProcedure
	getTrace   = pitwallv1connect.PitwallServiceGetTraceProcedure
)

// AC1 (R1, R6): /metrics is 200 in the text format with the series, is read
// only and takes no token, and a path of the web app still answers its file.
func TestMetricsAreServedAheadOfTheWebApp(t *testing.T) {
	r := newMetricsRig(t, true)
	if status, _, body := r.get(t, "/favicon.svg", nil); status != http.StatusOK || string(body) != "<svg/>" {
		t.Fatalf("a path of the web app: %d %q", status, body)
	}
	body := r.scrape(t)
	for _, want := range []string{
		"# TYPE pitwall_http_request_seconds histogram",
		`pitwall_http_requests_total{method="GET",route="static",status="200"} 1`,
		"# TYPE pitwall_trace_bytes histogram",
		"# TYPE pitwall_trace_points histogram",
		`pitwall_upstream_errors_total{reason="timeout",upstream="logbook"} 0`,
		"go_goroutines",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics lacks %q", want)
		}
	}
	if strings.Contains(body, "<") {
		t.Error("/metrics answered the web app's index.html")
	}
	if strings.Contains(body, `route="/metrics"`) {
		t.Error("a scrape counted itself as a route")
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, r.url+"/metrics", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /metrics: %d, want 405", resp.StatusCode)
	}
}

// AC2 (R2): two GetSession calls for different ids add 2 to one series, and no
// label holds an id, a lap, a path of the web app or an odd method.
func TestRequestsCountUnderOneRouteWhateverTheID(t *testing.T) {
	r := newMetricsRig(t, true)
	r.logbook.Reply(logbooktest.SessionsGet, &storev1.GetSessionResponse{
		Session: &storev1.SessionRow{SessionId: "x"}, DataVersion: "1:1",
	})
	for _, id := range []string{"20261017-162118", "01JB9QZ4V3X8M2K7T5R1N6PDWA"} {
		if status, _, _ := r.call(t, getSession, &pitwallv1.GetSessionRequest{SessionId: id}, nil); status != http.StatusOK {
			t.Fatalf("GetSession %s: %d", id, status)
		}
	}
	r.get(t, "/sessions/20261017-162118", nil)
	r.get(t, "/healthz", nil)
	r.get(t, "/twr.pitwall.v1.PitwallService/Nope", nil)
	body := r.scrape(t)
	expect(t, body, `pitwall_http_requests_total{method="GET",route="`+getSession+`",status="200"}`, "2")
	expect(t, body, `pitwall_http_request_seconds_count{method="GET",route="`+getSession+`"}`, "2")
	expect(t, body, `pitwall_http_requests_total{method="GET",route="static",status="200"}`, "1")
	expect(t, body, `pitwall_http_requests_total{method="GET",route="other",status="200"}`, "1")
	expect(t, body, `pitwall_http_requests_total{method="GET",route="other",status="404"}`, "1")
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, "pitwall_") && (strings.Contains(line, "2026") || strings.Contains(line, "01JB9") || strings.Contains(line, "/sessions")) {
			t.Errorf("a label holds a value of the request: %s", line)
		}
	}
}

// AC3 (R3): a call answered 304 counts under status="304".
func TestNotModifiedCountsUnderItsStatus(t *testing.T) {
	r := newMetricsRig(t, true)
	r.logbook.Reply(logbooktest.SessionsGet, &storev1.GetSessionResponse{
		Session: &storev1.SessionRow{SessionId: "s"}, DataVersion: "5:1829",
	})
	req := &pitwallv1.GetSessionRequest{SessionId: "s"}
	status, hdr, _ := r.call(t, getSession, req, nil)
	tag := hdr.Get("ETag")
	if status != http.StatusOK || tag == "" {
		t.Fatalf("first GET: %d, ETag %q", status, tag)
	}
	if status, _, _ = r.call(t, getSession, req, map[string]string{"If-None-Match": tag}); status != http.StatusNotModified {
		t.Fatalf("GET with the ETag: %d, want 304", status)
	}
	body := r.scrape(t)
	expect(t, body, `pitwall_http_requests_total{method="GET",route="`+getSession+`",status="304"}`, "1")
	expect(t, body, `pitwall_http_requests_total{method="GET",route="`+getSession+`",status="200"}`, "1")
}

// AC4 (R4): a GetTrace call observes its size and points, and a 304 does not.
func TestATraceObservesItsSize(t *testing.T) {
	r := newMetricsRig(t, true)
	r.logbook.Reply(logbooktest.LapsTrace, &storev1.GetTraceResponse{
		Trace: &storev1.LapTrace{
			SessionId: "s", Lap: 1,
			T: []float64{0, 1, 2, 3}, SpeedKph: []float64{100, 110, 120, 130},
		},
		DataVersion: "5:1829",
	})
	req := &pitwallv1.GetTraceRequest{SessionId: "s", Lap: 1}
	_, hdr, body := r.call(t, getTrace, req, nil)
	if status, _, _ := r.call(t, getTrace, req, map[string]string{"If-None-Match": hdr.Get("ETag")}); status != http.StatusNotModified {
		t.Fatalf("second GetTrace: %d, want 304", status)
	}
	var got pitwallv1.GetTraceResponse
	if err := protojson.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	size := proto.Size(&got)
	scraped := r.scrape(t)
	expect(t, scraped, "pitwall_trace_bytes_count", "1")
	expect(t, scraped, "pitwall_trace_bytes_sum", strconv.Itoa(size))
	expect(t, scraped, "pitwall_trace_points_count", "1")
	expect(t, scraped, "pitwall_trace_points_sum", "4")
}

// AC5 (R5): with logbook stopped, a data call increments the logbook series; a
// logbook that answers too late counts as a timeout.
func TestLogbookStoppedCountsAnUpstreamError(t *testing.T) {
	r := newMetricsRig(t, false)
	if status, _, _ := r.call(t, getSession, &pitwallv1.GetSessionRequest{SessionId: "s"}, nil); status != http.StatusServiceUnavailable {
		t.Fatalf("GetSession with no logbook: %d, want 503", status)
	}
	body := r.scrape(t)
	expect(t, body, `pitwall_upstream_errors_total{reason="unavailable",upstream="logbook"}`, "1")
	expect(t, body, `pitwall_http_requests_total{method="GET",route="`+getSession+`",status="503"}`, "1")
}
