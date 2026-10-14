package main

import (
	"slices"
	"strings"
)

// BoardRow is one leaderboard line: a player's standing plus how their rank
// moved since the previous play day.
type BoardRow struct {
	Standing
	Rank int
	Move int  // places gained since the previous play day; negative = dropped
	New  bool // first appearance on the board this play day
}

func (b BoardRow) Up() bool   { return !b.New && b.Move > 0 }
func (b BoardRow) Down() bool { return !b.New && b.Move < 0 }
func (b BoardRow) Same() bool { return !b.New && b.Move == 0 }

// AbsMove is Move without its sign, for "↓2".
func (b BoardRow) AbsMove() int { return max(b.Move, -b.Move) }

// sortStandings orders best first: most points, then fewer matches (same
// points from fewer games ranks higher), then name. Ties never share a rank.
func sortStandings(s []Standing) {
	slices.SortStableFunc(s, func(a, b Standing) int {
		if a.Points != b.Points {
			return b.Points - a.Points
		}
		if a.Played != b.Played {
			return a.Played - b.Played
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
}

// buildBoard ranks cur and compares with prev. With no previous play day
// (hasPrev false) every row is unchanged rather than new, like a first week.
func buildBoard(cur, prev []Standing, hasPrev bool) []BoardRow {
	sortStandings(cur)
	sortStandings(prev)
	prevRank := make(map[string]int, len(prev))
	for i, s := range prev {
		prevRank[s.ParticipantID] = i + 1
	}
	rows := make([]BoardRow, len(cur))
	for i, s := range cur {
		row := BoardRow{Standing: s, Rank: i + 1}
		if pr, ok := prevRank[s.ParticipantID]; ok {
			row.Move = pr - row.Rank
		} else if hasPrev {
			row.New = true
		}
		rows[i] = row
	}
	return rows
}
