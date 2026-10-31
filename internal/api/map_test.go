package api

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/those-who-run/contracts/gen/go/twr/events/v1"
	pitwallv1 "github.com/those-who-run/contracts/gen/go/twr/pitwall/v1"
	storev1 "github.com/those-who-run/contracts/gen/go/twr/store/v1"
)

func TestKindKeysAreTheSessionTypesNames(t *testing.T) {
	for typ, key := range map[eventsv1.SessionType]string{
		eventsv1.SessionType_SESSION_TYPE_UNSPECIFIED: "unknown",
		eventsv1.SessionType_SESSION_TYPE_UNKNOWN:     "unknown",
		eventsv1.SessionType_SESSION_TYPE_LAP_RACE:    "lap_race",
		eventsv1.SessionType_SESSION_TYPE_RANKED:      "ranked",
		eventsv1.SessionType_SESSION_TYPE_SOLO_TIMED:  "solo_timed",
		eventsv1.SessionType(99):                      "unknown",
	} {
		if got := KindKey(typ); got != key {
			t.Errorf("KindKey(%v) = %q, want %q", typ, got, key)
		}
	}
	for _, typ := range []eventsv1.SessionType{
		eventsv1.SessionType_SESSION_TYPE_LAP_RACE, eventsv1.SessionType_SESSION_TYPE_TIMED_FIELD,
		eventsv1.SessionType_SESSION_TYPE_RANKED, eventsv1.SessionType_SESSION_TYPE_RACE,
	} {
		if got, ok := kindType(KindKey(typ)); !ok || got != typ {
			t.Errorf("kindType(KindKey(%v)) = %v, %v", typ, got, ok)
		}
	}
	for _, bad := range []string{"", "unspecified", "LAP_RACE", "sprint"} {
		if _, ok := kindType(bad); ok {
			t.Errorf("kindType(%q) names a kind", bad)
		}
	}
	if kindFilterLabel("sprint") != "sprint" || kindFilterLabel("ranked") != "qualifying or practice" {
		t.Error("kindFilterLabel")
	}
}

func TestTheTrackIsNamedOnlyWhenTheDetectorIsSure(t *testing.T) {
	id := int32(442)
	for _, c := range []struct {
		track *eventsv1.TrackVerdict
		name  string
		sure  bool
	}{
		{nil, UnknownTrack, false},
		{&eventsv1.TrackVerdict{TrackId: &id, Name: "Suzuka", Confidence: eventsv1.TrackConfidence_TRACK_CONFIDENCE_PROBABLE}, "Suzuka", true},
		{&eventsv1.TrackVerdict{TrackId: &id, Name: "Fuji", Family: "Fuji Speedway", Confidence: eventsv1.TrackConfidence_TRACK_CONFIDENCE_AMBIGUOUS}, "Fuji Speedway", false},
		{&eventsv1.TrackVerdict{TrackId: &id, Name: "Fuji", Confidence: eventsv1.TrackConfidence_TRACK_CONFIDENCE_NONE}, UnknownTrack, false},
	} {
		got := Session(&storev1.SessionRow{Summary: &eventsv1.SessionSummary{Track: c.track}})
		if got.GetTrack().GetName() != c.name || got.GetTrack().GetSure() != c.sure {
			t.Errorf("%v: track %v, want %q sure %v", c.track, got.GetTrack(), c.name, c.sure)
		}
		if c.track != nil && got.GetTrack().GetTrackId() != 442 {
			t.Errorf("%v: the id is kept, sure or not: %v", c.track, got.GetTrack())
		}
	}
}

// An open session has no end, fuel burnt or driving time yet: absent, not zero,
// and its result is "Running", the pit wall's own word (v1's data.py).
func TestAnOpenSessionHasWhatItHasSoFar(t *testing.T) {
	laps := int32(6)
	row := &storev1.SessionRow{
		SessionId: "s",
		Summary: &eventsv1.SessionSummary{
			StartedAt:     timestamppb.Now(),
			EndedAt:       timestamppb.Now(), // never set while open; ignored if it were
			LapsCompleted: 1,
			TotalLaps:     &laps,
			SessionType:   eventsv1.SessionType_SESSION_TYPE_UNSPECIFIED,
			Kind:          &eventsv1.SessionKind{SessionType: eventsv1.SessionType_SESSION_TYPE_LAP_RACE, FuelUse: eventsv1.FuelUse_FUEL_USE_OFF},
		},
	}
	got := Session(row)
	if got.EndedAt != nil || got.FuelBurned != nil || got.DrivingMs != nil || got.PausedMs != nil || got.FuelByStint != nil {
		t.Errorf("open session: %v", got)
	}
	if got.GetKind() != "lap_race" || got.GetTotalLaps() != 6 || got.FuelUse == nil || got.GetFuelUse() {
		t.Errorf("open session's kind, laps or fuel use: %v", got)
	}
	if got.GetResult() != "Running, 1 lap" {
		t.Errorf("result %q", got.GetResult())
	}
	row.Closed = true
	row.Summary.FuelBurned = 12.5
	row.Summary.Gaps = []*eventsv1.SessionGap{{Lap: 3}, {Lap: 0}}
	got = Session(row)
	if got.GetFuelBurned() != 12.5 || len(got.GetFuelByStint()) != 1 || got.DrivingMs == nil || got.PausedMs == nil || got.EndedAt == nil {
		t.Errorf("closed session: %v", got)
	}
	if !got.GetContinued() || len(got.GetRestartLaps()) != 1 || got.GetRestartLaps()[0] != 3 {
		t.Errorf("restarts: %v", got)
	}
	if got.GetResult() != "1 lap" {
		t.Errorf("result %q", got.GetResult())
	}
	if lapsText(3) != "3 laps" {
		t.Error("lapsText(3)")
	}
}

func TestLapFlags(t *testing.T) {
	best := int32(2)
	pos := int32(4)
	row := &storev1.SessionRow{Summary: &eventsv1.SessionSummary{
		BestLap: &best,
		Gaps:    []*eventsv1.SessionGap{{Lap: 4}},
		Kind:    &eventsv1.SessionKind{FuelUse: eventsv1.FuelUse_FUEL_USE_OFF},
	}}
	lap := func(n int32, ms int32, complete bool, role eventsv1.PitRole) *storev1.LapRow {
		return &storev1.LapRow{Lap: &eventsv1.LapCompleted{Lap: n, LapTimeMs: ms, Complete: complete, PitRole: role, Position: &pos}}
	}
	got := Laps(row, []*storev1.LapRow{
		lap(1, 90000, true, eventsv1.PitRole_PIT_ROLE_NONE),
		lap(2, 88000, true, eventsv1.PitRole_PIT_ROLE_UNSPECIFIED),
		lap(3, 99000, true, eventsv1.PitRole_PIT_ROLE_IN_AND_OUT),
		lap(4, 0, true, eventsv1.PitRole_PIT_ROLE_NONE),
		lap(5, 30000, false, eventsv1.PitRole_PIT_ROLE_NONE),
	})
	want := []*pitwallv1.LapRow{
		{Lap: 1, Clean: true},
		{Lap: 2, Best: true, Clean: true},
		{Lap: 3, Pit: true, OutLap: true},
		{Lap: 4, Invalid: true, Restart: true},
		{Lap: 5, Invalid: true},
	}
	for i, w := range want {
		g := got[i]
		if g.GetPit() != w.GetPit() || g.GetOutLap() != w.GetOutLap() || g.GetInvalid() != w.GetInvalid() ||
			g.GetBest() != w.GetBest() || g.GetRestart() != w.GetRestart() || g.GetClean() != w.GetClean() {
			t.Errorf("lap %d: %v, want %v", w.GetLap(), g, w)
		}
		if g.GetFuelUse() || g.Position != nil {
			t.Errorf("lap %d: fuel use off and no field, but %v", w.GetLap(), g)
		}
	}
	if got[3].TimeMs != nil {
		t.Errorf("an untimed lap has a time: %v", got[3])
	}
}

func TestFilterOptionsAreNamedAndOrdered(t *testing.T) {
	got := Options(&storev1.FilterCounts{
		Track: []*storev1.ValueCount{{Value: "Suzuka", Count: 1}, {Value: "", Count: 2}, {Value: "Fuji", Count: 3}},
		Car:   []*storev1.ValueCount{{Value: "b"}, {Value: "a"}},
		Kind: []*storev1.KindCount{
			{SessionType: eventsv1.SessionType_SESSION_TYPE_SOLO_TIMED},
			{SessionType: eventsv1.SessionType(99)},
			{SessionType: eventsv1.SessionType_SESSION_TYPE_LAP_RACE, Count: 4},
		},
	})
	want := &pitwallv1.FilterOptions{
		Track: []*pitwallv1.FilterOption{{Value: "Fuji", Count: 3}, {Value: "Suzuka", Count: 1}, {Value: UnknownTrack, Count: 2}},
		Car:   []*pitwallv1.FilterOption{{Value: "a"}, {Value: "b"}},
		Kind: []*pitwallv1.KindOption{
			{Value: "unknown", Label: "unknown"},
			{Value: "lap_race", Label: "lap race", Count: 4},
			{Value: "solo_timed", Label: "time trial or practice"},
		},
	}
	if !proto.Equal(got, want) {
		t.Errorf("Options = %v\nwant %v", got, want)
	}
	if kindOrder("sprint") != len(kindLabels) {
		t.Error("a key not listed goes last")
	}
}
