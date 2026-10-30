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

// dayMonth renders "11 Okt"; weekday "Min" / "Sun".
func dayMonth(lang string, t time.Time) string {
	months := strings.Split(translate(lang, "month.short"), ",")
	m := t.Format("Jan")
	if len(months) == 12 {
		m = months[t.Month()-1]
	}
	return t.Format("2") + " " + m
}

func weekday(lang string, t time.Time) string {
	days := strings.Split(translate(lang, "weekday.short"), ",")
	if len(days) == 7 {
		return days[t.Weekday()]
	}
	return t.Format("Mon")
}

// monthYear renders "Oktober 2026".
func monthYear(lang string, t time.Time) string {
	months := strings.Split(translate(lang, "month.long"), ",")
	m := t.Format("January")
	if len(months) == 12 {
		m = months[t.Month()-1]
	}
	return m + " " + t.Format("2006")
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
