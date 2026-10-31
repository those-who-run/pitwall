package api

import (
	"cmp"
	"slices"
	"strings"

	"github.com/those-who-run/contracts/format"
	eventsv1 "github.com/those-who-run/contracts/gen/go/twr/events/v1"
	pitwallv1 "github.com/those-who-run/contracts/gen/go/twr/pitwall/v1"
	storev1 "github.com/those-who-run/contracts/gen/go/twr/store/v1"
)

// The pit wall's view rules: logbook keeps ecu's session and laps as ecu
// published them and decides nothing (the maintainer's decision D6, 2026-10-31),
// so the names, flags and labels the page shows are worked out here, as v1's
// pitwall/data.py worked them out from its store.

// UnknownTrack is the track's name when the detector is not sure of it, and the
// track filter's value for those sessions (logbook's empty track name).
const UnknownTrack = "Unknown track"

// kindPrefix is the prefix a SessionType's name drops to become its key.
const kindPrefix = "SESSION_TYPE_"

// unknownKind is the key of a session whose kind was never recorded.
const unknownKind = "unknown"

// kindLabels are the kind filter's names (v1's sessionkind.LABELS, with v2's
// ranked kind), in the order the filter lists them. A kind not here is named by
// its key.
var kindLabels = []struct{ key, label string }{
	{unknownKind, "unknown"},
	{"lap_race", "lap race"},
	{"solo_laps", "solo laps"},
	{"timed_field", "timed race or qualifying"},
	{"ranked", "qualifying or practice"},
	{"solo_timed", "time trial or practice"},
	{"race", "race"},
	{"time_trial", "time trial"},
	{"practice", "practice"},
}

// KindKey is a SessionType's key, as Session's kind has it: its name without
// SESSION_TYPE_, in lower case; "unknown" for an unspecified one.
func KindKey(t eventsv1.SessionType) string {
	if t == eventsv1.SessionType_SESSION_TYPE_UNSPECIFIED {
		return unknownKind
	}
	name, ok := eventsv1.SessionType_name[int32(t)]
	if !ok {
		return unknownKind
	}
	return strings.ToLower(strings.TrimPrefix(name, kindPrefix))
}

// kindType is the SessionType a kind key names, and whether it names one.
func kindType(key string) (eventsv1.SessionType, bool) {
	if key == unknownKind {
		return eventsv1.SessionType_SESSION_TYPE_UNKNOWN, true
	}
	v, ok := eventsv1.SessionType_value[kindPrefix+strings.ToUpper(key)]
	if !ok || v == int32(eventsv1.SessionType_SESSION_TYPE_UNSPECIFIED) || strings.ToLower(key) != key {
		return 0, false
	}
	return eventsv1.SessionType(v), true
}

// kindFilterLabel is the kind filter's name for a key.
func kindFilterLabel(key string) string {
	for _, k := range kindLabels {
		if k.key == key {
			return k.label
		}
	}
	return key
}

// kindOrder is a key's place in the kind filter: kindLabels' order, then any
// other key.
func kindOrder(key string) int {
	for i, k := range kindLabels {
		if k.key == key {
			return i
		}
	}
	return len(kindLabels)
}

// sessionKey is the session's kind key: the summary's session type, or its
// kind's when that is the one set.
func sessionKey(s *eventsv1.SessionSummary) string {
	t := s.GetSessionType()
	if t == eventsv1.SessionType_SESSION_TYPE_UNSPECIFIED {
		t = s.GetKind().GetSessionType()
	}
	return KindKey(t)
}

// sure is whether the detector placed the track for certain enough to name it:
// probable or confirmed (v1's sure_track_id).
func sure(t *eventsv1.TrackVerdict) bool {
	c := t.GetConfidence()
	return c == eventsv1.TrackConfidence_TRACK_CONFIDENCE_PROBABLE || c == eventsv1.TrackConfidence_TRACK_CONFIDENCE_CONFIRMED
}

// restartLaps are the laps an engineer restart fell in (v1's restart_laps).
func restartLaps(s *eventsv1.SessionSummary) []int32 {
	var laps []int32
	for _, g := range s.GetGaps() {
		if g.GetLap() > 0 {
			laps = append(laps, g.GetLap())
		}
	}
	return laps
}

// positive is v, or nil when v is not above zero: the summary's zero for a value
// it does not have.
func positive[T int32 | float64](v T) *T {
	if v > 0 {
		return &v
	}
	return nil
}

// summary is the row's summary, or an empty one, so its optional fields can be
// read as fields.
func summary(row *storev1.SessionRow) *eventsv1.SessionSummary {
	if s := row.GetSummary(); s != nil {
		return s
	}
	return &eventsv1.SessionSummary{}
}

// Session is logbook's session row as the API's Session (v1's session_values).
// The best lap is ecu's (the maintainer's decision, 2026-10-30: the card and the
// pit wall show ecu's best lap, not v1's card rule).
func Session(row *storev1.SessionRow) *pitwallv1.Session {
	s := summary(row)
	kind := s.GetKind()
	closed := row.GetClosed()
	key := sessionKey(s)
	track := s.GetTrack()
	out := &pitwallv1.Session{
		SessionId: row.GetSessionId(),
		StartedAt: s.GetStartedAt(),
		Closed:    closed,
		Kind:      key,
		Track: &pitwallv1.Track{
			Name: format.TrackLabel(track),
			Sure: sure(track),
		},
		CarCode:       positive(s.GetCarCode()),
		Finished:      s.GetFinished(),
		FinalPosition: s.FinalPosition,
		TotalCars:     s.TotalCars,
		PositionShown: format.PositionShown(kind),
		TotalTimeMs:   s.TotalTimeMs,
		BestLapMs:     s.BestLapTimeMs,
		BestLap:       s.BestLap,
		LapsCompleted: s.GetLapsCompleted(),
		FuelCapacity:  positive(s.GetFuelCapacity()),
		PitStops:      int32(len(s.GetPitStops())), //nolint:gosec // a session's stops are a handful
		JoinedOnLap:   s.JoinedOnLap,
		RestartLaps:   restartLaps(s),
		CardPostedAt:  row.GetCardPostedAt(),
		FromV1Rows:    row.FromV1Rows,
	}
	out.Continued = len(s.GetGaps()) > 0
	if closed {
		out.EndedAt = s.GetEndedAt()
	}
	if track != nil {
		out.Track.TrackId = track.TrackId
		if e := track.GetEvidence(); e != "" {
			out.Track.Evidence = &e
		}
	}
	if car := s.GetCarName(); car != "" {
		out.Car = &car
	}
	if n := s.GetTotalLaps(); n > 0 {
		out.TotalLaps = &n
	}
	switch kind.GetFuelUse() {
	case eventsv1.FuelUse_FUEL_USE_ON:
		out.FuelUse = new(true)
	case eventsv1.FuelUse_FUEL_USE_OFF:
		out.FuelUse = new(false)
	}
	// An open session's summary has no fuel burnt, driving time or end yet
	// (store.proto's SessionRow): absent, not zero.
	if closed {
		burned := s.GetFuelBurned()
		out.FuelBurned = &burned
		out.FuelByStint = s.GetFuelBurnedByStint()
		if len(out.FuelByStint) == 0 && burned > 0 {
			out.FuelByStint = []float64{burned}
		}
		driving := s.GetDrivingMs()
		out.DrivingMs = &driving
	}
	if paused := s.GetPausedMs(); closed || paused > 0 {
		out.PausedMs = &paused
	}
	text := cardText(row)
	out.Title, out.Result, out.KindLabel = text.title, text.result, text.kindLabel
	return out
}

// Sessions maps each row.
func Sessions(rows []*storev1.SessionRow) []*pitwallv1.Session {
	out := make([]*pitwallv1.Session, 0, len(rows))
	for _, r := range rows {
		out = append(out, Session(r))
	}
	return out
}

// Laps are logbook's laps as the lap table, with their flags (v1's
// lap_summaries): pit and out from ecu's pit role, partial and untimed laps
// invalid, the best lap ecu's, the restart laps from the session's gaps, and
// the position only where the session shows one.
func Laps(row *storev1.SessionRow, laps []*storev1.LapRow) []*pitwallv1.LapRow {
	s := summary(row)
	fuelOff := s.GetKind().GetFuelUse() == eventsv1.FuelUse_FUEL_USE_OFF
	showPosition := format.PositionShown(s.GetKind())
	restarts := restartLaps(s)
	out := make([]*pitwallv1.LapRow, 0, len(laps))
	for _, l := range laps {
		lap := l.GetLap()
		role := lap.GetPitRole()
		fuelStart, fuelEnd, fuelUsed := lap.GetFuelAtStart(), lap.GetFuelAtEnd(), lap.GetFuelUsed()
		r := &pitwallv1.LapRow{
			Lap:         lap.GetLap(),
			TimeMs:      positive(lap.GetLapTimeMs()),
			Complete:    lap.GetComplete(),
			FuelStart:   &fuelStart,
			FuelEnd:     &fuelEnd,
			FuelUsed:    &fuelUsed,
			Frames:      l.GetFrames(),
			TopSpeedKph: l.TopSpeedKph,
			Pit:         role == eventsv1.PitRole_PIT_ROLE_IN_LAP || role == eventsv1.PitRole_PIT_ROLE_IN_AND_OUT,
			OutLap:      role == eventsv1.PitRole_PIT_ROLE_OUT_LAP || role == eventsv1.PitRole_PIT_ROLE_IN_AND_OUT,
			Invalid:     !lap.GetComplete() || lap.GetLapTimeMs() <= 0,
			Best:        s.BestLap != nil && lap.GetLap() == s.GetBestLap(),
			FuelUse:     !fuelOff,
			Restart:     slices.Contains(restarts, lap.GetLap()),
		}
		if showPosition {
			r.Position = lap.Position
		}
		r.Clean = !r.Pit && !r.OutLap && !r.Invalid && !r.Restart
		out = append(out, r)
	}
	return out
}

// Trace is logbook's lap trace as the API's, channel for channel.
func Trace(t *storev1.LapTrace) *pitwallv1.Trace {
	return &pitwallv1.Trace{
		SessionId:   t.GetSessionId(),
		Lap:         t.GetLap(),
		T:           t.GetT(),
		SpeedKph:    t.GetSpeedKph(),
		ThrottlePct: t.GetThrottlePct(),
		BrakePct:    t.GetBrakePct(),
		Gear:        t.GetGear(),
		Rpm:         t.GetRpm(),
		X:           t.GetX(),
		Z:           t.GetZ(),
	}
}

// Options are logbook's filter counts as the filters' values: the empty track
// name is UnknownTrack, each kind gets its key and name, tracks and cars are in
// name order and kinds in kindLabels' (v1's filter_options).
func Options(c *storev1.FilterCounts) *pitwallv1.FilterOptions {
	out := &pitwallv1.FilterOptions{}
	for _, v := range c.GetTrack() {
		name := v.GetValue()
		if name == "" {
			name = UnknownTrack
		}
		out.Track = append(out.Track, &pitwallv1.FilterOption{Value: name, Count: v.GetCount()})
	}
	for _, v := range c.GetCar() {
		out.Car = append(out.Car, &pitwallv1.FilterOption{Value: v.GetValue(), Count: v.GetCount()})
	}
	for _, v := range c.GetKind() {
		key := KindKey(v.GetSessionType())
		out.Kind = append(out.Kind, &pitwallv1.KindOption{Value: key, Label: kindFilterLabel(key), Count: v.GetCount()})
	}
	byValue := func(a, b *pitwallv1.FilterOption) int { return cmp.Compare(a.GetValue(), b.GetValue()) }
	slices.SortStableFunc(out.Track, byValue)
	slices.SortStableFunc(out.Car, byValue)
	slices.SortStableFunc(out.Kind, func(a, b *pitwallv1.KindOption) int {
		return cmp.Or(cmp.Compare(kindOrder(a.GetValue()), kindOrder(b.GetValue())), cmp.Compare(a.GetValue(), b.GetValue()))
	})
	return out
}
