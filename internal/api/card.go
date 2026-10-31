package api

import (
	"strconv"

	storev1 "github.com/those-who-run/contracts/gen/go/twr/store/v1"
)

// card is the words a session's results card has for it: Session's title,
// result and kind_label.
type card struct {
	title, result, kindLabel string
}

// cardText is the card's words for the row.
//
// TODO(contracts format): this is a stand-in. The card's title, result and
// kind label are podium's, and move into contracts' format package as
// Title(summary), Result(summary, endReason, closed, lastLap) and
// KindLabel(kind, totalLaps), with cases in format-cases.json (contracts#41,
// "Follow-up: the card builder in contracts/format"). Once a contracts release
// has them, this calls them and the stand-in goes, so the page and the card
// can't drift (ADR 0004 point 5). Until then the stand-in names the track, the
// kind as the kind filter does and the laps, and copies none of podium's card
// rules. Only "Running, N laps" for a session still open is the pit wall's own
// word (v1's pitwall/data.py), and stays.
func cardText(row *storev1.SessionRow) card {
	s := row.GetSummary()
	label := kindFilterLabel(sessionKey(s))
	laps := lapsText(s.GetLapsCompleted())
	result := laps
	if !row.GetClosed() {
		result = "Running, " + laps
	}
	return card{title: trackLabel(s.GetTrack()), result: result, kindLabel: label}
}

// lapsText is "1 lap" or "N laps".
func lapsText(n int32) string {
	if n == 1 {
		return "1 lap"
	}
	return strconv.Itoa(int(n)) + " laps"
}
