package api

import (
	"github.com/those-who-run/contracts/format"
	storev1 "github.com/those-who-run/contracts/gen/go/twr/store/v1"
)

// card is the words the results card has for a session: Session's title,
// result and kind_label.
type card struct {
	title, result, kindLabel string
}

// cardText is the card's words for the row, from contracts' format package,
// which podium's results card calls too, so the page and the card can't name a
// session differently (ADR 0004 point 5). The result is the row's: closed and
// the end reason from logbook, and the last lap logbook stored, for a session
// ecu's stop ended; the format package adds the pit wall's own words for a
// session still open or with no full lap.
func cardText(row *storev1.SessionRow) card {
	s := summary(row)
	return card{
		title:     format.Title(s),
		result:    format.Result(s, row.GetEndReason(), row.GetClosed(), int(row.GetLastLap())),
		kindLabel: format.KindLabel(s.GetKind().GetSessionType(), int(s.GetTotalLaps())),
	}
}
