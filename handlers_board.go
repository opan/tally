package main

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
)

// tallyVM is the overview: the leaderboard of one season as of one play day.
type tallyVM struct {
	tallyCtx
	ShareURL   string
	Periods    []Period
	Period     *Period // selected season; nil when the tally has none
	Rounds     []Round // of the selected season, newest first
	Round      *Round  // "as of" play day; nil before the first one
	Board      []BoardRow
	Results    []*Match // scored matches of the selected play day
	HasPlayers bool
}

func (a *App) handleTally(w http.ResponseWriter, r *http.Request) {
	tc, ok := a.loadTally(w, r, "overview")
	if !ok {
		return
	}
	if vm, ok := a.loadBoard(w, r, tc); ok {
		a.render(w, r, "tally", vm)
	}
}

// loadBoard picks the season (?season=) and play day (?day=) and builds the
// leaderboard. ok is false once an error response has been written.
func (a *App) loadBoard(w http.ResponseWriter, r *http.Request, tc tallyCtx) (vm tallyVM, ok bool) {
	ctx := r.Context()
	vm = tallyVM{tallyCtx: tc, ShareURL: baseURL(r) + tallyPath(tc.Tally, "")}

	var err error
	if vm.Periods, err = a.store.ListPeriods(ctx, tc.Tally.ID); err != nil {
		internalError(w, "list periods", err)
		return vm, false
	}
	if tc.IsAdmin() {
		players, err := a.store.ListParticipants(ctx, tc.Tally.ID)
		if err != nil {
			internalError(w, "list players", err)
			return vm, false
		}
		vm.HasPlayers = len(players) > 0
	}

	q := r.URL.Query()
	for i := range vm.Periods {
		if vm.Periods[i].ID == q.Get("season") {
			vm.Period = &vm.Periods[i]
		}
	}
	if vm.Period == nil {
		cur, err := a.store.CurrentPeriod(ctx, tc.Tally.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			internalError(w, "current period", err)
			return vm, false
		}
		vm.Period = cur
	}
	if vm.Period == nil {
		return vm, true
	}

	// Only play days with scored matches count as board snapshots.
	all, err := a.store.RoundsForPeriod(ctx, vm.Period.ID)
	if err != nil {
		internalError(w, "list rounds", err)
		return vm, false
	}
	for _, rd := range all {
		if rd.Awards > 0 {
			vm.Rounds = append(vm.Rounds, rd)
		}
	}
	sel := 0
	for i, rd := range vm.Rounds {
		if rd.ID == q.Get("day") {
			sel = i
		}
	}
	if len(vm.Rounds) == 0 {
		return vm, true
	}
	vm.Round = &vm.Rounds[sel]

	cur, err := a.store.StandingsAsOf(ctx, vm.Period.ID, vm.Round.PlayedOn)
	if err != nil {
		internalError(w, "standings", err)
		return vm, false
	}
	var prev []Standing
	hasPrev := sel+1 < len(vm.Rounds)
	if hasPrev {
		if prev, err = a.store.StandingsAsOf(ctx, vm.Period.ID, vm.Rounds[sel+1].PlayedOn); err != nil {
			internalError(w, "standings", err)
			return vm, false
		}
	}
	if tc.Tally.Preset().IsCounter() {
		useEntries(cur)
		useEntries(prev)
	}
	vm.Board = buildBoard(cur, prev, hasPrev)

	matches, err := a.store.MatchesForRound(ctx, tc.Tally.ID, vm.Round.ID)
	if err != nil {
		internalError(w, "round matches", err)
		return vm, false
	}
	for _, m := range matches {
		if !m.Scheduled() {
			vm.Results = append(vm.Results, m)
		}
	}
	return vm, true
}

// useEntries makes "played" count every entry, for counter tallies where
// there are no matches.
func useEntries(s []Standing) {
	for i := range s {
		s[i].Played = s[i].Entries
	}
}
