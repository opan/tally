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
	if err := s.AddScore(ctx, tl.ID, u.ID, r, "", "Setepanus", 41, 7); err != nil {
		t.Fatal(err)
	}
	if err := s.AddScore(ctx, tl.ID, u.ID, r, "", "setepanus", 41, 7); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("same new name twice: got %v, want ErrNameTaken", err)
	}
	present, _ := s.Attendance(ctx, r.ID)
	if len(present) != 2 {
		t.Errorf("present after scores = %d, want 2 (Aaron, Setepanus)", len(present))
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
	if sp := got["Setepanus"]; sp.Points != 41 || sp.Played != 1 {
		t.Errorf("Setepanus = %d points, %d played; want 41, 1", sp.Points, sp.Played)
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
