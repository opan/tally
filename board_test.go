package main

import (
	"testing"
	"time"
)

func st(id string, pts, played int) Standing {
	return Standing{ParticipantID: id, Name: id, Points: pts, Played: played}
}

func TestBuildBoard(t *testing.T) {
	prev := []Standing{st("dhamar", 300, 7), st("dimas", 290, 7), st("aaron", 280, 7), st("qq", 250, 6)}
	cur := []Standing{st("dhamar", 487, 12), st("dimas", 486, 12), st("qq", 432, 11), st("aaron", 399, 11), st("yeri", 42, 1)}
	rows := buildBoard(cur, prev, true)

	want := []struct {
		id         string
		rank, move int
		new        bool
	}{
		{"dhamar", 1, 0, false},
		{"dimas", 2, 0, false},
		{"qq", 3, 1, false},
		{"aaron", 4, -1, false},
		{"yeri", 5, 0, true},
	}
	for i, w := range want {
		r := rows[i]
		if r.ParticipantID != w.id || r.Rank != w.rank || r.Move != w.move || r.New != w.new {
			t.Errorf("row %d = %s rank %d move %d new %v; want %+v", i, r.ParticipantID, r.Rank, r.Move, r.New, w)
		}
	}
	if !rows[2].Up() || !rows[3].Down() || !rows[0].Same() || rows[4].Up() || rows[4].Same() {
		t.Error("arrow helpers disagree with Move/New")
	}
}

func TestBuildBoardFirstPlayDayHasNoNewMarkers(t *testing.T) {
	rows := buildBoard([]Standing{st("a", 42, 1), st("b", 36, 1)}, nil, false)
	for _, r := range rows {
		if r.New || !r.Same() {
			t.Errorf("%s: first play day should show unchanged, got %+v", r.ParticipantID, r)
		}
	}
}

func TestSortStandingsTieBreaks(t *testing.T) {
	s := []Standing{st("zed", 84, 2), st("Amy", 84, 3), st("bob", 84, 2), st("cat", 90, 3)}
	sortStandings(s)
	got := []string{s[0].Name, s[1].Name, s[2].Name, s[3].Name}
	want := []string{"cat", "bob", "zed", "Amy"} // points, then fewer played, then name
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestWeekLabel(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	for _, c := range []struct {
		r    Round
		want string
	}{
		{Round{PlayedOn: day(1), WeekPart: 1, WeekDays: 1}, "1"},
		{Round{PlayedOn: day(7), WeekPart: 1, WeekDays: 1}, "1"},
		{Round{PlayedOn: day(8), WeekPart: 1, WeekDays: 1}, "2"},
		{Round{PlayedOn: day(22), WeekPart: 1, WeekDays: 2}, "4A"},
		{Round{PlayedOn: day(25), WeekPart: 2, WeekDays: 2}, "4B"},
		{Round{PlayedOn: day(30), WeekPart: 1, WeekDays: 1}, "5"},
	} {
		if got := c.r.WeekLabel(); got != c.want {
			t.Errorf("%s part %d of %d: got %q, want %q", c.r.PlayedOn.Format("2 Jan"), c.r.WeekPart, c.r.WeekDays, got, c.want)
		}
	}
}
