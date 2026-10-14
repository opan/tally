package main

import (
	"strings"
	"time"
)

const dateLayout = "2006-01-02"

var jakarta = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		return time.FixedZone("WIB", 7*3600)
	}
	return loc
}()

// today is the current calendar date in Jakarta, as a UTC-midnight time so it
// compares cleanly with Postgres date columns.
func today() time.Time {
	y, m, d := time.Now().In(jakarta).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func parseDate(s string) (time.Time, bool) {
	t, err := time.Parse(dateLayout, strings.TrimSpace(s))
	return t, err == nil
}

// formatDate renders "11 Okt 2026" / "11 Oct 2026" using the locale's month names.
func formatDate(lang string, t time.Time) string {
	months := strings.Split(translate(lang, "month.short"), ",")
	m := t.Format("Jan")
	if len(months) == 12 {
		m = months[t.Month()-1]
	}
	return t.Format("2") + " " + m + " " + t.Format("2006")
}
