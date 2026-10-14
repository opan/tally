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
