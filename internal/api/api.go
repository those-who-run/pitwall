// Package api is the pit wall's Connect service, twr.pitwall.v1.PitwallService
// (paddock#124): each data method asks logbook over NATS for its twr.store.v1
// answer and maps it to the API's messages (the maintainer's decision D6,
// 2026-10-31), with an ETag from logbook's data_version and a 304 when the
// browser already has the answer. GetEngineStatus answers from the live KV
// (paddock#125, internal/engine). The server holds no data of its own.
package api

import (
	"context"
	"slices"
	"strings"
	"time"

	connect "connectrpc.com/connect/v2"
	pitwallv1 "github.com/those-who-run/contracts/gen/go/twr/pitwall/v1"
	"github.com/those-who-run/contracts/gen/go/twr/pitwall/v1/pitwallv1connect"
	storev1 "github.com/those-who-run/contracts/gen/go/twr/store/v1"
)

// pageLimit is how many sessions the pit wall asks logbook for at a time: the
// most logbook answers in one reply (contracts' store.proto).
const pageLimit = 500

// pageTries is how often a list starts again when logbook's data changes
// between its pages, before the last try's answer is used as it is.
const pageTries = 3

// The trace's points: 0 is logbook's default, 1500, and at most 10000
// (GetTraceRequest).
const maxPoints = 10000

// Engine is the engineer's status as the live KV has it now (internal/engine).
type Engine interface {
	Status() *pitwallv1.EngineStatus
}

// Server serves PitwallService.
type Server struct {
	pitwallv1connect.UnimplementedPitwallServiceHandler

	store   store
	sources []string
	version string
	// etagScope is what every ETag's key starts from besides the request: the
	// pit wall's version and the sources it shows, sorted.
	etagScope string
	engine    Engine
}

// New is the service over logbook, reached through nc, for the sources in
// sources (TWR_PITWALL_SRC). version is the pit wall's, in every ETag, so a new
// pit wall never answers 304 for an old one's body. timeout is how long logbook
// has to answer; 0 is StoreTimeout.
func New(nc Requester, sources []string, version string, engine Engine, timeout time.Duration) *Server {
	if timeout <= 0 {
		timeout = StoreTimeout
	}
	return &Server{
		store:     store{nc: nc, timeout: timeout},
		sources:   sources,
		version:   version,
		etagScope: etagScope(version, sources),
		engine:    engine,
	}
}

// etagScope is the pit wall's version and its sources, sorted, so a pit wall
// showing other sources (TWR_PITWALL_SRC) never answers 304 for a body built
// from another set, whatever their order.
func etagScope(version string, sources []string) string {
	sorted := slices.Sorted(slices.Values(sources))
	return version + "\x00" + strings.Join(sorted, ",")
}

// Register registers the service on server.
func (s *Server) Register(server *connect.Server) {
	pitwallv1connect.RegisterPitwallServiceHandler(server, s)
}

// sessionPage is a page of a list of sessions: ListSessionsResponse and
// ListSameTrackResponse.
type sessionPage interface {
	GetUnchanged() bool
	GetDataVersion() string
	GetSessions() []*storev1.SessionRow
	GetMatched() int32
}

// collect reads a list a page at a time, with read asking logbook for the page
// from offset, from known, the caller's data_version, which only the first page
// is sent. It returns every page's sessions and the first page, whose unchanged
// says the caller's version is still current. A page with a data_version that is
// not the first's means the data changed between them: it starts again, up to
// pageTries times, then keeps the last try's.
func collect[P sessionPage](read func(offset int32, known *string) (P, error), known *string) ([]*storev1.SessionRow, P, error) {
	var rows []*storev1.SessionRow
	var first P
	for try := 1; ; try++ {
		rows = nil
		changed := false
		var err error
		if first, err = read(0, known); err != nil || first.GetUnchanged() {
			return nil, first, err
		}
		page := first
		for {
			rows = append(rows, page.GetSessions()...)
			if len(page.GetSessions()) == 0 || int32(len(rows)) >= page.GetMatched() { //nolint:gosec // pages of at most 500
				break
			}
			if page, err = read(int32(len(rows)), nil); err != nil { //nolint:gosec // as above
				return nil, first, err
			}
			if page.GetDataVersion() != first.GetDataVersion() {
				changed = true
				break
			}
		}
		if !changed || try >= pageTries {
			return rows, first, nil
		}
	}
}

// ListSessions is the session list, read from logbook a page at a time.
func (s *Server) ListSessions(ctx context.Context, req *pitwallv1.ListSessionsRequest) (*pitwallv1.ListSessionsResponse, error) {
	want, err := s.sessionsRequest(req)
	if err != nil {
		return nil, err
	}
	v := newView(ctx, s.etagScope, pitwallv1connect.PitwallServiceListSessionsProcedure, req)
	rows, first, err := collect(func(offset int32, known *string) (*storev1.ListSessionsResponse, error) {
		want.Offset, want.KnownDataVersion = offset, known
		var page storev1.ListSessionsResponse
		return &page, s.store.call(ctx, subjectSessionsList, want, &page)
	}, v.known)
	if err != nil {
		return nil, err
	}
	if err := v.answer(first.GetDataVersion(), first.GetUnchanged()); err != nil {
		return nil, err
	}
	return &pitwallv1.ListSessionsResponse{
		Sessions: Sessions(rows),
		Options:  Options(first.GetOptions()),
		Total:    first.GetTotal(),
		Matched:  int32(len(rows)), //nolint:gosec // a list of sessions is far below 2^31
	}, nil
}

// sessionsRequest is the list's filters as logbook's request: the track's
// UnknownTrack is logbook's empty name, and the kind's key its SessionType.
func (s *Server) sessionsRequest(req *pitwallv1.ListSessionsRequest) (*storev1.ListSessionsRequest, error) {
	want := &storev1.ListSessionsRequest{
		Sources:  s.sources,
		FromDay:  req.GetFromDay(),
		ToDay:    req.GetToDay(),
		TimeZone: req.GetTimeZone(),
		Limit:    pageLimit,
	}
	if track := req.GetTrack(); track != "" {
		if track == UnknownTrack {
			track = ""
		}
		want.Track = &track
	}
	if car := req.GetCar(); car != "" {
		want.Car = &car
	}
	if key := req.GetKind(); key != "" {
		t, ok := kindType(key)
		if !ok {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "kind %q is not a session kind", key)
		}
		want.SessionType = t
	}
	for _, day := range []struct{ name, value string }{{"from_day", want.GetFromDay()}, {"to_day", want.GetToDay()}} {
		if day.value == "" {
			continue
		}
		if _, err := time.Parse(time.DateOnly, day.value); err != nil {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "%s is a date, YYYY-MM-DD", day.name)
		}
	}
	if tz := want.GetTimeZone(); tz != "" {
		if _, err := time.LoadLocation(tz); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, "time_zone is a time zone name, Europe/Dublin")
		}
	}
	return want, nil
}

// needSession is invalid_argument for an empty session id.
func needSession(id string) error {
	if id == "" {
		return connect.NewError(connect.CodeInvalidArgument, "session_id is needed")
	}
	return nil
}

// GetSession is one session.
func (s *Server) GetSession(ctx context.Context, req *pitwallv1.GetSessionRequest) (*pitwallv1.GetSessionResponse, error) {
	if err := needSession(req.GetSessionId()); err != nil {
		return nil, err
	}
	v := newView(ctx, s.etagScope, pitwallv1connect.PitwallServiceGetSessionProcedure, req)
	var resp storev1.GetSessionResponse
	err := s.store.call(ctx, subjectSessionsGet, &storev1.GetSessionRequest{
		Sources: s.sources, SessionId: req.GetSessionId(), KnownDataVersion: v.known,
	}, &resp)
	if err != nil {
		return nil, err
	}
	if err := v.answer(resp.GetDataVersion(), resp.GetUnchanged()); err != nil {
		return nil, err
	}
	return &pitwallv1.GetSessionResponse{Session: Session(resp.GetSession())}, nil
}

// ListLaps is a session's lap table.
func (s *Server) ListLaps(ctx context.Context, req *pitwallv1.ListLapsRequest) (*pitwallv1.ListLapsResponse, error) {
	if err := needSession(req.GetSessionId()); err != nil {
		return nil, err
	}
	v := newView(ctx, s.etagScope, pitwallv1connect.PitwallServiceListLapsProcedure, req)
	var resp storev1.ListLapsResponse
	err := s.store.call(ctx, subjectLapsList, &storev1.ListLapsRequest{
		Sources: s.sources, SessionId: req.GetSessionId(), KnownDataVersion: v.known,
	}, &resp)
	if err != nil {
		return nil, err
	}
	if err := v.answer(resp.GetDataVersion(), resp.GetUnchanged()); err != nil {
		return nil, err
	}
	return &pitwallv1.ListLapsResponse{
		SessionId: req.GetSessionId(),
		Laps:      Laps(resp.GetSession(), resp.GetLaps()),
	}, nil
}

// GetTrace is one lap's trace.
func (s *Server) GetTrace(ctx context.Context, req *pitwallv1.GetTraceRequest) (*pitwallv1.GetTraceResponse, error) {
	if err := needSession(req.GetSessionId()); err != nil {
		return nil, err
	}
	if req.GetLap() < 1 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "lap is from 1")
	}
	if p := req.GetPoints(); p < 0 || p > maxPoints {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "points is from 1 to %d, or 0 for the default", maxPoints)
	}
	v := newView(ctx, s.etagScope, pitwallv1connect.PitwallServiceGetTraceProcedure, req)
	var resp storev1.GetTraceResponse
	err := s.store.call(ctx, subjectLapsTrace, &storev1.GetTraceRequest{
		Sources: s.sources, SessionId: req.GetSessionId(), Lap: req.GetLap(), Points: req.GetPoints(),
		KnownDataVersion: v.known,
	}, &resp)
	if err != nil {
		return nil, err
	}
	if err := v.answer(resp.GetDataVersion(), resp.GetUnchanged()); err != nil {
		return nil, err
	}
	return &pitwallv1.GetTraceResponse{Trace: Trace(resp.GetTrace())}, nil
}

// ListSameTrack is the other sessions at the session's track, read a page at a
// time.
func (s *Server) ListSameTrack(ctx context.Context, req *pitwallv1.ListSameTrackRequest) (*pitwallv1.ListSameTrackResponse, error) {
	if err := needSession(req.GetSessionId()); err != nil {
		return nil, err
	}
	v := newView(ctx, s.etagScope, pitwallv1connect.PitwallServiceListSameTrackProcedure, req)
	want := &storev1.ListSameTrackRequest{Sources: s.sources, SessionId: req.GetSessionId(), Limit: pageLimit}
	rows, first, err := collect(func(offset int32, known *string) (*storev1.ListSameTrackResponse, error) {
		want.Offset, want.KnownDataVersion = offset, known
		var page storev1.ListSameTrackResponse
		return &page, s.store.call(ctx, subjectSameTrack, want, &page)
	}, v.known)
	if err != nil {
		return nil, err
	}
	if err := v.answer(first.GetDataVersion(), first.GetUnchanged()); err != nil {
		return nil, err
	}
	return &pitwallv1.ListSameTrackResponse{Sessions: Sessions(rows)}, nil
}

// GetEngineStatus is the engineer's status from the live KV, never cached
// (paddock#125 R5).
func (s *Server) GetEngineStatus(ctx context.Context, _ *pitwallv1.GetEngineStatusRequest) (*pitwallv1.GetEngineStatusResponse, error) {
	if info, ok := connect.CallInfoForServerContext(ctx); ok {
		info.ResponseHeader().Set("Cache-Control", "no-store")
	}
	status := &pitwallv1.EngineStatus{Activity: pitwallv1.Activity_ACTIVITY_OFFLINE}
	if s.engine != nil {
		status = s.engine.Status()
	}
	return &pitwallv1.GetEngineStatusResponse{Status: status}, nil
}
