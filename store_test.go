package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testStore connects to TALLY_TEST_DATABASE_URL (a throwaway database; it is
// wiped) or skips. With compose: createdb tally_test, then
// TALLY_TEST_DATABASE_URL=postgres://tally:tally@localhost:5433/tally_test?sslmode=disable
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TALLY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TALLY_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		t.Fatal(err)
	}
	return &Store{pool: pool}
}

func TestDeletePlayerWhoOnlyAttended(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx, "owner", "x")
	if err != nil {
		t.Fatal(err)
	}
	tl, err := s.CreateTally(ctx, u.ID, Tally{Slug: "club", Name: "Club", Template: "badminton_doubles", Visibility: VisibilityLink})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AddParticipants(ctx, tl.ID, u.ID, []string{"Dhamar", "Dimas", "QQ", "Ferdian", "Aaron"}); err != nil {
		t.Fatal(err)
	}
	players, _ := s.ListParticipants(ctx, tl.ID)
	id := map[string]string{}
	var all []string
	for _, p := range players {
		id[p.Name] = p.ID
		all = append(all, p.ID)
	}
	d := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	period, err := s.CreatePeriod(ctx, u.ID, Period{TallyID: tl.ID, Name: "S1", StartsOn: d, EndsOn: d.AddDate(0, 3, 0), Scoring: DefaultContinuous})
	if err != nil {
		t.Fatal(err)
	}
	roundID, err := s.CreateRound(ctx, tl.ID, u.ID, period.ID, d, 1, all)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.RoundByID(ctx, tl.ID, roundID)

	// Aaron plays a match; Dhamar was only ticked as present.
	res := MatchResult{
		Players: [2][]string{{id["Aaron"], id["Dimas"]}, {id["QQ"], id["Ferdian"]}},
		Games:   [2][]int{{42}, {36}},
	}
	res.Outcome, _ = DefaultContinuous.Score(res.Games[0], res.Games[1])
	if _, err := s.SaveMatch(ctx, tl.ID, u.ID, r, nil, res); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteParticipant(ctx, tl.ID, u.ID, id["Dhamar"]); err != nil {
		t.Fatalf("deleting a player who only attended: %v", err)
	}
	if err := s.DeleteParticipant(ctx, tl.ID, u.ID, id["Aaron"]); !errors.Is(err, ErrInUse) {
		t.Fatalf("deleting a player with a match: got %v, want ErrInUse", err)
	}
	present, _ := s.Attendance(ctx, roundID)
	if len(present) != 4 {
		t.Errorf("attendance after delete: %d players, want 4", len(present))
	}
}

func TestAddScore(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx, "owner", "x")
	if err != nil {
		t.Fatal(err)
	}
	tl, err := s.CreateTally(ctx, u.ID, Tally{Slug: "club", Name: "Club", Template: "badminton_doubles", Visibility: VisibilityLink})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AddParticipants(ctx, tl.ID, u.ID, []string{"Aaron", "Dimas", "QQ", "Ferdian"}); err != nil {
		t.Fatal(err)
	}
	players, _ := s.ListParticipants(ctx, tl.ID)
	id := map[string]string{}
	for _, p := range players {
		id[p.Name] = p.ID
	}
	d := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	period, err := s.CreatePeriod(ctx, u.ID, Period{TallyID: tl.ID, Name: "S1", StartsOn: d, EndsOn: d.AddDate(0, 3, 0), Scoring: DefaultContinuous})
	if err != nil {
		t.Fatal(err)
	}
	roundID, err := s.CreateRound(ctx, tl.ID, u.ID, period.ID, d, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.RoundByID(ctx, tl.ID, roundID)

	// Aaron plays one match, then gets points entered for six more plays.
	res := MatchResult{
		Players: [2][]string{{id["Aaron"], id["Dimas"]}, {id["QQ"], id["Ferdian"]}},
		Games:   [2][]int{{42}, {36}},
	}
	res.Outcome, _ = DefaultContinuous.Score(res.Games[0], res.Games[1])
	if _, err := s.SaveMatch(ctx, tl.ID, u.ID, r, nil, res); err != nil {
		t.Fatal(err)
	}
	for _, pts := range []int{42, 40, 42, 0, 42, 30} {
		if err := s.AddScore(ctx, tl.ID, u.ID, r, id["Aaron"], "", pts, 7); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddScore(ctx, tl.ID, u.ID, r, id["Aaron"], "", 42, 7); !errors.Is(err, ErrPlayCap) {
		t.Fatalf("8th play: got %v, want ErrPlayCap", err)
	}

	// A new player is added with their first score and counts as present.
	if err := s.AddScore(ctx, tl.ID, u.ID, r, "", "Eko", 41, 7); err != nil {
		t.Fatal(err)
	}
	if err := s.AddScore(ctx, tl.ID, u.ID, r, "", "eko", 41, 7); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("same new name twice: got %v, want ErrNameTaken", err)
	}
	present, _ := s.Attendance(ctx, r.ID)
	if len(present) != 2 {
		t.Errorf("present after scores = %d, want 2 (Aaron, Eko)", len(present))
	}

	// An extra is not a play; scores are.
	if err := s.AddAwards(ctx, tl.ID, u.ID, r, []AwardInput{{ParticipantID: id["Aaron"], Points: 5}}, "bonus"); err != nil {
		t.Fatal(err)
	}
	st, err := s.StandingsAsOf(ctx, period.ID, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Standing{}
	for _, x := range st {
		got[x.Name] = x
	}
	if a := got["Aaron"]; a.Points != 42+196+5 || a.Played != 7 || a.Entries != 8 {
		t.Errorf("Aaron = %d points, %d played, %d entries; want 243, 7, 8", a.Points, a.Played, a.Entries)
	}
	if sp := got["Eko"]; sp.Points != 41 || sp.Played != 1 {
		t.Errorf("Eko = %d points, %d played; want 41, 1", sp.Points, sp.Played)
	}

	scores, _ := s.ScoreAwards(ctx, r.ID)
	extras, _ := s.ManualAwards(ctx, r.ID)
	if len(scores) != 7 || len(extras) != 1 {
		t.Fatalf("scores %d, extras %d; want 7, 1", len(scores), len(extras))
	}
	if _, kind, err := s.DeleteAward(ctx, tl.ID, u.ID, scores[0].ID); err != nil || kind != AwardScore {
		t.Fatalf("delete score: kind %q, err %v", kind, err)
	}
	if err := s.AddScore(ctx, tl.ID, u.ID, r, id["Aaron"], "", 42, 7); err != nil {
		t.Errorf("after deleting one, Aaron can play again: %v", err)
	}
}

func TestRoundWeekLabels(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, _ := s.CreateUser(ctx, "owner", "x")
	tl, err := s.CreateTally(ctx, u.ID, Tally{Slug: "club", Name: "Club", Template: "badminton_doubles", Visibility: VisibilityLink})
	if err != nil {
		t.Fatal(err)
	}
	day := func(m time.Month, d int) time.Time { return time.Date(2026, m, d, 0, 0, 0, 0, time.UTC) }
	period, err := s.CreatePeriod(ctx, u.ID, Period{TallyID: tl.ID, Name: "S1", StartsOn: day(10, 1), EndsOn: day(12, 31), Scoring: DefaultContinuous})
	if err != nil {
		t.Fatal(err)
	}
	// 30 Oct and 1 Nov share a calendar week but not a week of the month.
	want := map[string]string{"2026-10-11": "2", "2026-10-22": "4A", "2026-10-25": "4B", "2026-10-30": "5", "2026-11-01": "1"}
	ids := map[string]string{}
	for d := range want {
		on, _ := parseDate(d)
		if ids[d], err = s.CreateRound(ctx, tl.ID, u.ID, period.ID, on, 1, nil); err != nil {
			t.Fatal(err)
		}
	}
	rounds, err := s.RoundsForPeriod(ctx, period.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rounds {
		d := r.PlayedOn.Format(dateLayout)
		if got := r.WeekLabel(); got != want[d] {
			t.Errorf("RoundsForPeriod %s: got %q, want %q", d, got, want[d])
		}
		one, err := s.RoundByID(ctx, tl.ID, ids[d])
		if err != nil {
			t.Fatal(err)
		}
		if got := one.WeekLabel(); got != want[d] {
			t.Errorf("RoundByID %s: got %q, want %q", d, got, want[d])
		}
	}
}

func TestDayStandingsAndSeasonStats(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, _ := s.CreateUser(ctx, "owner", "x")
	tl, err := s.CreateTally(ctx, u.ID, Tally{Slug: "club", Name: "Club", Template: "badminton_doubles", Visibility: VisibilityLink})
	if err != nil {
		t.Fatal(err)
	}
	d1 := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	period, _ := s.CreatePeriod(ctx, u.ID, Period{TallyID: tl.ID, Name: "S1", StartsOn: d1, EndsOn: d1.AddDate(0, 3, 0), Scoring: DefaultContinuous})
	id1, _ := s.CreateRound(ctx, tl.ID, u.ID, period.ID, d1, 1, nil)
	id2, _ := s.CreateRound(ctx, tl.ID, u.ID, period.ID, d1.AddDate(0, 0, 7), 1, nil)
	r1, _ := s.RoundByID(ctx, tl.ID, id1)
	r2, _ := s.RoundByID(ctx, tl.ID, id2)
	for _, e := range []struct {
		r    *Round
		name string
		pts  int
	}{{r1, "Aaron", 42}, {r1, "Aaron", 40}, {r1, "Dimas", 42}, {r2, "Dimas", 42}, {r2, "Dimas", 41}} {
		players, _ := s.ListParticipants(ctx, tl.ID)
		id, newName := "", e.name
		for _, p := range players {
			if p.Name == e.name {
				id, newName = p.ID, ""
			}
		}
		if err := s.AddScore(ctx, tl.ID, u.ID, e.r, id, newName, e.pts, 7); err != nil {
			t.Fatal(err)
		}
	}
	day2, err := s.StandingsForRound(ctx, r2.ID)
	if err != nil || len(day2) != 1 || day2[0].Name != "Dimas" || day2[0].Points != 83 || day2[0].Played != 2 {
		t.Fatalf("day 2 standings = %+v, %v; want only Dimas 83 in 2", day2, err)
	}
	stats, players, err := s.SeasonDayStats(ctx, period.ID)
	if err != nil {
		t.Fatal(err)
	}
	if players != 2 {
		t.Errorf("season players = %d, want 2", players)
	}
	if got := stats[r1.ID]; got != (DayStats{Players: 2, Plays: 3, Points: 124, TopName: "Aaron", TopPoints: 82}) {
		t.Errorf("day 1 stats = %+v", got)
	}
}
