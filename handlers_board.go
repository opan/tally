package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

// tallyVM is the overview: the standings of one season as of one play day.
type tallyVM struct {
	tallyCtx
	ShareURL   string
	Periods    []Period
	Period     *Period // selected season; nil when the tally has none
	Rounds     []Round // of the selected season with points, newest first
	Round      *Round  // the filtered play day, else the latest one; nil before the first
	Day        *Round  // set when the board shows one play day only (?day=)
	Standings  []BoardRow
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
	vm = tallyVM{tallyCtx: tc}

	if err := a.seasonBoard(r, &vm.tallyCtx); err != nil {
		internalError(w, "season leaderboard", err)
		return vm, false
	}
	vm.ShareURL = baseURL(r) + vm.BoardPath("")
	var err error
	if vm.Periods, err = a.store.ListPeriods(ctx, tc.Tally.ID, vm.Board.ID); err != nil {
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
		cur, err := a.store.CurrentPeriod(ctx, tc.Tally.ID, vm.Board.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			internalError(w, "current period", err)
			return vm, false
		}
		vm.Period = cur
	}
	vm.Periods = pickerPeriods(vm.Periods, vm.Period)
	if vm.Period == nil {
		return vm, true
	}

	// Only play days with points are on the board.
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
	if len(vm.Rounds) == 0 {
		return vm, true
	}
	vm.Round = &vm.Rounds[0]
	for i, rd := range vm.Rounds {
		if rd.ID == q.Get("day") {
			vm.Round, vm.Day = &vm.Rounds[i], &vm.Rounds[i]
		}
	}

	// The whole season, with movement since the play day before the latest;
	// or, filtered, the points of that one play day.
	var cur, prev []Standing
	hasPrev := false
	if vm.Day != nil {
		cur, err = a.store.StandingsForRound(ctx, vm.Day.ID)
	} else {
		cur, err = a.store.StandingsAsOf(ctx, vm.Period.ID, time.Time{})
		if hasPrev = len(vm.Rounds) > 1; hasPrev && err == nil {
			prev, err = a.store.StandingsAsOf(ctx, vm.Period.ID, vm.Rounds[1].PlayedOn)
		}
	}
	if err != nil {
		internalError(w, "standings", err)
		return vm, false
	}
	if vm.Board.Preset().IsCounter() {
		useEntries(cur)
		useEntries(prev)
	}
	vm.Standings = buildBoard(cur, prev, hasPrev)

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

// seasonBoard points a page with ?season= at that season's leaderboard. An
// unknown season is left to the page, which shows the current one instead.
func (a *App) seasonBoard(r *http.Request, tc *tallyCtx) error {
	id := r.URL.Query().Get("season")
	if id == "" {
		return nil
	}
	p, err := a.store.PeriodByID(r.Context(), tc.Tally.ID, id)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	tc.boardOf(p)
	return nil
}

// pickerPeriods drops archived seasons from a season picker, except the one
// on screen (opened from the seasons page), so it still shows as selected.
func pickerPeriods(all []Period, selected *Period) []Period {
	var out []Period
	for _, p := range all {
		if !p.Archived() || (selected != nil && p.ID == selected.ID) {
			out = append(out, p)
		}
	}
	return out
}

// useEntries makes "played" count every entry, for counter tallies where
// there are no matches.
func useEntries(s []Standing) {
	for i := range s {
		s[i].Played = s[i].Entries
	}
}
