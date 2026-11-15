package api

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nats-io/nats.go"

	"github.com/those-who-run/pitwall/internal/metrics"
)

// R5: what a failed request to logbook is counted as.
func TestUpstreamFailureNamesTheUpstreamAndReason(t *testing.T) {
	for _, c := range []struct {
		err              error
		upstream, reason string
	}{
		{nats.ErrNoResponders, metrics.UpstreamLogbook, metrics.ReasonUnavailable},
		{context.DeadlineExceeded, metrics.UpstreamLogbook, metrics.ReasonTimeout},
		{fmt.Errorf("wrapped: %w", nats.ErrTimeout), metrics.UpstreamLogbook, metrics.ReasonTimeout},
		{nats.ErrConnectionClosed, metrics.UpstreamNATS, metrics.ReasonUnavailable},
		{nats.ErrDisconnected, metrics.UpstreamNATS, metrics.ReasonUnavailable},
		{nats.ErrNoServers, metrics.UpstreamNATS, metrics.ReasonUnavailable},
		{errors.New("something else"), metrics.UpstreamNATS, metrics.ReasonOther},
	} {
		if up, reason := upstreamFailure(c.err); up != c.upstream || reason != c.reason {
			t.Errorf("%v: %s/%s, want %s/%s", c.err, up, reason, c.upstream, c.reason)
		}
	}
}

// A server with no metrics, as the tests build it, counts nothing and never panics.
func TestNoMetricsIsHarmless(*testing.T) {
	s := &store{}
	s.countReply("503")
	s.countReply("404")
	var m *metrics.Metrics
	m.TraceAnswered(1, 1)
	m.UpstreamError(metrics.UpstreamNATS, metrics.ReasonOther)
}
