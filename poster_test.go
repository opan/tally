package main

import (
	"testing"
	"time"
)

func TestSplitTitle(t *testing.T) {
	for in, want := range map[string][2]string{
		"Bala Bulu Point Rank":         {"Bala Bulu", "Point Rank"},
		"Liga Pingpong Kantor Jakarta": {"Liga Pingpong", "Kantor Jakarta"},
		"Smash Club":                   {"Smash", "Club"},
		"Solo":                         {"Solo", ""},
	} {
		a, b := splitTitle(in)
		if a != want[0] || b != want[1] {
			t.Errorf("splitTitle(%q) = %q, %q; want %q", in, a, b, want)
		}
	}
}

func TestMonthRange(t *testing.T) {
	d := func(s string) time.Time { v, _ := parseDate(s); return v }
	p := pageData{Lang: "id"}
	for _, c := range []struct{ from, to, want string }{
		{"2026-10-01", "2026-12-31", "Okt – Des 2026"},
		{"2026-11-01", "2027-01-31", "Nov 2026 – Jan 2027"},
		{"2026-10-01", "2026-10-31", "Okt 2026"},
	} {
		if got := p.MonthRange(d(c.from), d(c.to)); got != c.want {
			t.Errorf("%s..%s = %q, want %q", c.from, c.to, got, c.want)
		}
	}
}
