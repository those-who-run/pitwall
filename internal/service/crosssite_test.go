package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	"github.com/those-who-run/contracts/gen/go/twr/pitwall/v1/pitwallv1connect"
	storev1 "github.com/those-who-run/contracts/gen/go/twr/store/v1"

	"github.com/those-who-run/pitwall/internal/api"
	"github.com/those-who-run/pitwall/internal/logbooktest"
	"github.com/those-who-run/pitwall/internal/natstest"
)

// ADR 0014 point 8 (paddock): the pit wall has no login, and what keeps another
// site's page from deleting a session through the viewer's browser is that
// Connect takes only JSON or protobuf bodies by POST. A form or a text/plain
// body, which a page can send cross-site with no preflight, is refused; a JSON
// body needs a CORS preflight, which the pit wall never answers with an allow;
// and a write is never a GET, which an <img> or a link could send. These tests
// hold that, over the whole handler.

// writes are the methods with side effects.
var writes = []string{
	pitwallv1connect.PitwallServiceDeleteSessionProcedure,
	pitwallv1connect.PitwallServiceRestoreSessionProcedure,
}

// writeServer is the whole handler over a logbook stand-in whose delete and
// restore succeed, so a refusal is the pit wall's, not logbook's.
func writeServer(t *testing.T) (*httptest.Server, *logbooktest.Fake) {
	t.Helper()
	s := natstest.Bare(t)
	nc, err := nats.Connect(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	fake := logbooktest.Start(t, nc)
	row := &storev1.SessionRow{SessionId: "s", Closed: true}
	fake.Set(logbooktest.SessionsDelete, func([]byte) (proto.Message, error) {
		return &storev1.DeleteSessionResponse{Session: row, DataVersion: "2"}, nil
	})
	fake.Set(logbooktest.SessionsRestore, func([]byte) (proto.Message, error) {
		return &storev1.RestoreSessionResponse{Session: row, DataVersion: "3"}, nil
	})
	srv := httptest.NewServer(testHandler(t, Deps{API: api.New(nc, DefaultSources, "test", nil, 0)}))
	t.Cleanup(srv.Close)
	return srv, fake
}

// asked is how many delete and restore requests logbook has had.
func asked(f *logbooktest.Fake) int {
	return len(f.Requests(logbooktest.SessionsDelete)) + len(f.Requests(logbooktest.SessionsRestore))
}

// send sends method to srv's path with body and headers, from another site.
func send(t *testing.T, srv *httptest.Server, method, path, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://elsewhere.example")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp
}

// noCORS fails the test for any CORS header on resp.
func noCORS(t *testing.T, what string, resp *http.Response) {
	t.Helper()
	for name := range resp.Header {
		if strings.HasPrefix(strings.ToLower(name), "access-control-") {
			t.Errorf("%s: answered %s: %q", what, name, resp.Header.Get(name))
		}
	}
}

// The control: a same-origin JSON POST is a write logbook gets, so the
// refusals below are the content type's and the method's.
func TestAJSONPostDeletes(t *testing.T) {
	srv, fake := writeServer(t)
	for _, path := range writes {
		resp := send(t, srv, http.MethodPost, path, `{"sessionId":"s"}`, map[string]string{"Content-Type": "application/json"})
		if resp.StatusCode != http.StatusOK {
			t.Errorf("a JSON POST to %s: %d, want 200", path, resp.StatusCode)
		}
	}
	if n := asked(fake); n != len(writes) {
		t.Errorf("logbook was asked %d times, want %d", n, len(writes))
	}
}

// A body a page can send cross-site without a preflight (a form's, or
// text/plain) is refused, and logbook is never asked.
func TestADeleteWithAFormOrTextBodyIsRefused(t *testing.T) {
	srv, fake := writeServer(t)
	bodies := map[string]string{
		"text/plain":                        `{"sessionId":"s"}`,
		"text/plain;charset=UTF-8":          `{"sessionId":"s"}`,
		"application/x-www-form-urlencoded": `sessionId=s`,
		"multipart/form-data; boundary=x":   "--x\r\nContent-Disposition: form-data; name=\"sessionId\"\r\n\r\ns\r\n--x--\r\n",
		"":                                  `{"sessionId":"s"}`,
	}
	for _, path := range writes {
		for contentType, body := range bodies {
			headers := map[string]string{}
			if contentType != "" {
				headers["Content-Type"] = contentType
			}
			resp := send(t, srv, http.MethodPost, path, body, headers)
			if resp.StatusCode != http.StatusUnsupportedMediaType {
				t.Errorf("a POST to %s with Content-Type %q: %d, want 415", path, contentType, resp.StatusCode)
			}
			noCORS(t, "a "+contentType+" POST", resp)
		}
	}
	if n := asked(fake); n != 0 {
		t.Errorf("logbook was asked %d times", n)
	}
}

// A write is never a GET: Connect's GET form of the request is refused.
func TestADeleteByGetIsRefused(t *testing.T) {
	srv, fake := writeServer(t)
	q := url.Values{"connect": {"v1"}, "encoding": {"json"}, "message": {`{"sessionId":"s"}`}}
	for _, path := range writes {
		for _, target := range []string{path + "?" + q.Encode(), path} {
			resp := send(t, srv, http.MethodGet, target, "", nil)
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("GET %s: %d, want 405", target, resp.StatusCode)
			}
		}
	}
	if n := asked(fake); n != 0 {
		t.Errorf("logbook was asked %d times", n)
	}
}

// A CORS preflight for a JSON POST gets no allow headers, so the browser never
// sends the POST another site's page asks for.
func TestAPreflightGetsNoCORSAllowHeaders(t *testing.T) {
	srv, fake := writeServer(t)
	for _, path := range writes {
		resp := send(t, srv, http.MethodOptions, path, "", map[string]string{
			"Access-Control-Request-Method":  http.MethodPost,
			"Access-Control-Request-Headers": "content-type, connect-protocol-version",
		})
		noCORS(t, "OPTIONS "+path, resp)
		// Even a JSON POST sent anyway (not by a browser) is answered with no
		// allow, so a browser would never hand its answer to the page.
		resp = send(t, srv, http.MethodPost, path, `{"sessionId":"s"}`, map[string]string{"Content-Type": "application/json"})
		noCORS(t, "a cross-site JSON POST to "+path, resp)
	}
	if n := asked(fake); n != len(writes) {
		t.Errorf("logbook was asked %d times, want only the %d JSON POSTs", n, len(writes))
	}
}
