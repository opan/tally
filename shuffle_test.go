package main

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"time"
)

func players(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("p%02d", i)
	}
	return out
}

func TestSuggestMatchesShape(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	in := shuffleInput{Available: players(10), Courts: 2, SideSize: 2}
	matches, waiting := suggestMatches(in, rng)
	if len(matches) != 2 || len(waiting) != 2 {
		t.Fatalf("got %d matches, %d waiting", len(matches), len(waiting))
	}
	seen := map[string]bool{}
	for _, m := range matches {
		for _, side := range m {
			if len(side) != 2 {
				t.Fatalf("side size %d", len(side))
			}
			for _, p := range side {
				if seen[p] {
					t.Fatalf("%s picked twice", p)
				}
				seen[p] = true
			}
		}
	}
	for _, p := range waiting {
		if seen[p] {
			t.Fatalf("%s both playing and waiting", p)
		}
	}
}

func TestSuggestMatchesCapsCourtsByPlayers(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	matches, waiting := suggestMatches(shuffleInput{Available: players(7), Courts: 3, SideSize: 2}, rng)
	if len(matches) != 1 || len(waiting) != 3 {
		t.Fatalf("got %d matches, %d waiting", len(matches), len(waiting))
	}
	matches, _ = suggestMatches(shuffleInput{Available: players(3), Courts: 1, SideSize: 2}, rng)
	if len(matches) != 0 {
		t.Fatalf("3 players can't fill a doubles court, got %d", len(matches))
	}
	matches, _ = suggestMatches(shuffleInput{Available: players(5), Courts: 2, SideSize: 1}, rng)
	if len(matches) != 2 {
		t.Fatalf("singles: got %d matches", len(matches))
	}
}

func TestSuggestMatchesPrefersFewestPlayedThenLongestWait(t *testing.T) {
	all := players(6)
	base := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)
	in := shuffleInput{
		Available:   all,
		PlayedToday: map[string]int{"p00": 2, "p01": 2, "p02": 1, "p03": 1, "p04": 1, "p05": 0},
		// p02 played most recently, so it waits; p03/p04 waited longer.
		LastPlayed: map[string]time.Time{"p02": base.Add(time.Hour), "p03": base, "p04": base},
		Courts:     1, SideSize: 2,
	}
	for seed := range uint64(50) {
		matches, waiting := suggestMatches(in, rand.New(rand.NewPCG(seed, 7)))
		var got []string
		for _, side := range matches[0] {
			got = append(got, side...)
		}
		slices.Sort(got)
		// p05 (0 played), p03 and p04 (1 played, waited longest), then one of p02/p00/p01:
		// p02 has 1 played, so it beats the 2-played players.
		if want := []string{"p02", "p03", "p04", "p05"}; !slices.Equal(got, want) {
			t.Fatalf("seed %d: got %v, want %v (waiting %v)", seed, got, want, waiting)
		}
	}
}

// Over a session, rotation keeps everyone's match count within one of each other.
func TestSuggestMatchesRotationIsFair(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 42))
	all := players(10)
	played := map[string]int{}
	last := map[string]time.Time{}
	clock := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)
	for range 12 {
		matches, _ := suggestMatches(shuffleInput{Available: all, PlayedToday: played, LastPlayed: last, Courts: 2, SideSize: 2}, rng)
		clock = clock.Add(15 * time.Minute)
		for _, m := range matches {
			for _, side := range m {
				for _, p := range side {
					played[p]++
					last[p] = clock
				}
			}
		}
		lo, hi := 1<<30, 0
		for _, p := range all {
			lo, hi = min(lo, played[p]), max(hi, played[p])
		}
		if hi-lo > 1 {
			t.Fatalf("unfair rotation: counts %v", played)
		}
	}
}
