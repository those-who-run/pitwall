package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// R2: a method a client invents never becomes a label; a handler that writes
// nothing is a 200.
func TestMiddlewareKeepsLabelsToAFixedSet(t *testing.T) {
	m := New()
	h := m.Middleware(func(*http.Request) string { return RouteOther }, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for _, method := range []string{"GET", "BREW"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, "/x", nil))
	}
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`pitwall_http_requests_total{method="GET",route="other",status="200"} 1`,
		`pitwall_http_requests_total{method="other",route="other",status="200"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("no %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "BREW") {
		t.Error("an invented method became a label")
	}
}
