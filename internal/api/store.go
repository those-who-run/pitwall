package api

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	connect "connectrpc.com/connect/v2"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/those-who-run/pitwall/internal/metrics"
)

// logbook's endpoints (contracts' subjects.yaml, services; paddock#122 and #123).
const (
	subjectSessionsList = "svc.logbook.sessions.list"
	subjectSessionsGet  = "svc.logbook.sessions.get"
	subjectSameTrack    = "svc.logbook.sessions.same_track"
	subjectLapsList     = "svc.logbook.laps.list"
	subjectLapsTrace    = "svc.logbook.laps.trace"
	// The writes (ADR 0014 in paddock).
	subjectSessionsDelete  = "svc.logbook.sessions.delete"
	subjectSessionsRestore = "svc.logbook.sessions.restore"
)

// StoreTimeout is how long a call to logbook may take before the pit wall answers
// unavailable (paddock#124 R3: within 2 s).
const StoreTimeout = 2 * time.Second

// NATS micro's error headers, as logbook sets them on a reply that is an error.
const (
	errorHeader     = "Nats-Service-Error"
	errorCodeHeader = "Nats-Service-Error-Code"
)

// Requester is the part of a NATS connection the store calls need: *nats.Conn,
// or a test's stand-in.
type Requester interface {
	RequestMsgWithContext(ctx context.Context, msg *nats.Msg) (*nats.Msg, error)
}

// store calls logbook's micro endpoints over NATS request and reply, each in
// protobuf's JSON form with no other header (contracts' store.proto).
type store struct {
	nc      Requester
	timeout time.Duration
	metrics *metrics.Metrics
}

var (
	marshal   = protojson.MarshalOptions{}
	unmarshal = protojson.UnmarshalOptions{DiscardUnknown: true}
)

// call sends req to subject and decodes logbook's reply into resp. An error is a
// Connect error the handler can return as it is: logbook's 400 is
// invalid_argument, its 404 not_found, its 409 (deleting an open session)
// failed_precondition, and no answer within the timeout, no
// responder or NATS down is unavailable.
func (s *store) call(ctx context.Context, subject string, req, resp proto.Message) error {
	data, err := marshal.Marshal(req)
	if err != nil {
		return connect.Errorf(connect.CodeInternal, "encoding %s: %v", subject, err)
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	msg := nats.NewMsg(subject)
	msg.Data = data
	reply, err := s.nc.RequestMsgWithContext(ctx, msg)
	if err != nil {
		if ctxErr := context.Cause(ctx); ctxErr != nil && !errors.Is(ctxErr, context.DeadlineExceeded) {
			// The caller went away; there is no one to answer.
			return connect.NewError(connect.CodeCanceled, "the request was cancelled")
		}
		s.metrics.UpstreamError(upstreamFailure(err))
		return connect.Errorf(connect.CodeUnavailable, "logbook did not answer %s: %v", subject, err)
	}
	if code := reply.Header.Get(errorCodeHeader); code != "" {
		s.countReply(code)
		return storeError(subject, code, reply.Header.Get(errorHeader))
	}
	if err := unmarshal.Unmarshal(reply.Data, resp); err != nil {
		s.metrics.UpstreamError(metrics.UpstreamLogbook, metrics.ReasonOther)
		return connect.Errorf(connect.CodeInternal, "logbook's answer to %s: %v", subject, err)
	}
	return nil
}

// storeError is logbook's error reply as a Connect error.
func storeError(subject, code, text string) error {
	n, _ := strconv.Atoi(code)
	switch n {
	case 400:
		return connect.NewError(connect.CodeInvalidArgument, text)
	case 404:
		return connect.NewError(connect.CodeNotFound, text)
	case 409:
		return connect.NewError(connect.CodeFailedPrecondition, text)
	case 503:
		return connect.NewError(connect.CodeUnavailable, text)
	}
	return connect.NewError(connect.CodeInternal, fmt.Sprintf("logbook failed %s (%s): %s", subject, code, text))
}

// upstreamFailure says which upstream a failed request to logbook was a failure
// of, and why. NATS itself being down or closed is the pit wall's NATS failing;
// nobody subscribed to logbook's subject is logbook unavailable; running out of
// time is logbook's timeout, as NATS can't tell a slow logbook from a lost reply.
func upstreamFailure(err error) (upstream, reason string) {
	switch {
	case errors.Is(err, nats.ErrNoResponders):
		return metrics.UpstreamLogbook, metrics.ReasonUnavailable
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, nats.ErrTimeout):
		return metrics.UpstreamLogbook, metrics.ReasonTimeout
	case errors.Is(err, nats.ErrConnectionClosed), errors.Is(err, nats.ErrDisconnected),
		errors.Is(err, nats.ErrConnectionReconnecting), errors.Is(err, nats.ErrNoServers):
		return metrics.UpstreamNATS, metrics.ReasonUnavailable
	}
	return metrics.UpstreamNATS, metrics.ReasonOther
}

// countReply counts logbook's error reply when it is logbook failing: its 400,
// 404 and 409 are the caller's mistakes, answered as they are, not failures.
func (s *store) countReply(code string) {
	switch n, _ := strconv.Atoi(code); n {
	case 400, 404, 409:
	case 503:
		s.metrics.UpstreamError(metrics.UpstreamLogbook, metrics.ReasonUnavailable)
	default:
		s.metrics.UpstreamError(metrics.UpstreamLogbook, metrics.ReasonOther)
	}
}
