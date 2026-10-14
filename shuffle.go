package main

import (
	"math/rand/v2"
	"slices"
	"time"
)

// shuffleInput describes who can be put on court right now.
type shuffleInput struct {
	Available   []string             // present and not on court
	PlayedToday map[string]int       // matches (scheduled or done) this round
	LastPlayed  map[string]time.Time // latest match start this round; zero if none
	Courts      int                  // free courts to fill
	SideSize    int
}

// suggestMatches picks who plays next and pairs them at random. Selection is
// fair: fewest matches today first, then whoever has waited longest, then
// random. Pairing among the chosen is purely random.
func suggestMatches(in shuffleInput, rng *rand.Rand) (matches [][2][]string, waiting []string) {
	order := slices.Clone(in.Available)
	rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	slices.SortStableFunc(order, func(a, b string) int {
		if d := in.PlayedToday[a] - in.PlayedToday[b]; d != 0 {
			return d
		}
		return in.LastPlayed[a].Compare(in.LastPlayed[b])
	})

	per := 2 * in.SideSize
	n := 0
	if per > 0 {
		n = min(in.Courts, len(order)/per)
	}
	chosen, waiting := order[:n*per], order[n*per:]
	rng.Shuffle(len(chosen), func(i, j int) { chosen[i], chosen[j] = chosen[j], chosen[i] })
	for i := range n {
		m := chosen[i*per : (i+1)*per]
		matches = append(matches, [2][]string{slices.Clone(m[:in.SideSize]), slices.Clone(m[in.SideSize:])})
	}
	return matches, waiting
}
