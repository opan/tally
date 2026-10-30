package main

import (
	"net/http"
	"slices"
	"strings"
	"time"
)

// pairStat is how a player did with (partner) or against (opponent) someone.
type pairStat struct {
	ID     string
	Name   string
	Played int
	Won    int
}

// dayStat is a player's play day: points that day and rank after it.
type dayStat struct {
	Round   Round
	Points  int
	Matches int
	Rank    int // 0 when not ranked yet
	BarPct  int // points as a share of the best day, for the bar
}

type playerVM struct {
	tallyCtx
	Player    *Participant
	Periods   []Period
	Period    *Period
	Rank      int
	Of        int
	Standing  Standing
	Won       int
	Drawn     int
	Lost      int
	Days      []dayStat // newest first
	Matches   []*Match  // newest first
	Partners  []pairStat
	Opponents []pairStat
	Awards    []Award // extras
	Scores    int     // plays entered as points only (no match details)
	Counter   bool
	Admin     bool // the site admin's read-only view
}

// PlayerHref, DayHref and BackHref keep links inside the site admin's view
// when vm.Admin is set.
func (v playerVM) PlayerHref(id string) string {
	q := ""
	if v.Period != nil {
		q = "?season=" + v.Period.ID
	}
	if v.Admin {
		return "/admin/tallies/" + v.Tally.ID + "/players/" + id + q
	}
	return tallyPath(v.Tally, "/players/"+id) + q
}

func (v playerVM) DayHref(roundID string) string {
	if v.Admin {
		return "/admin/tallies/" + v.Tally.ID + "/days/" + roundID
	}
	return tallyPath(v.Tally, "?season="+v.Period.ID+"&day="+roundID)
}

func (v playerVM) BackHref() string {
	if v.Admin {
		return "/admin/tallies/" + v.Tally.ID
	}
	if v.Period != nil {
		return tallyPath(v.Tally, "?season="+v.Period.ID)
	}
	return tallyPath(v.Tally, "")
}

func sortPairs(m map[string]*pairStat) []pairStat {
	out := make([]pairStat, 0, len(m))
	for _, p := range m {
		out = append(out, *p)
	}
	slices.SortFunc(out, func(a, b pairStat) int {
		if a.Played != b.Played {
			return b.Played - a.Played
		}
		if a.Won != b.Won {
			return b.Won - a.Won
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out
}

func (a *App) handlePlayer(w http.ResponseWriter, r *http.Request) {
	tc, ok := a.loadTally(w, r, "overview")
	if !ok {
		return
	}
	ctx := r.Context()
	player, err := a.store.ParticipantByID(ctx, tc.Tally.ID, r.PathValue("pid"))
	if isNotFound(err) {
		a.notFound(w, r)
		return
	}
	if err != nil {
		internalError(w, "load player", err)
		return
	}
	a.playerPage(w, r, playerVM{tallyCtx: tc, Player: player})
}

// playerPage fills in a player's season (?season=, else the current one) and
// renders it; shared with the site admin's read-only view (vm.Admin).
func (a *App) playerPage(w http.ResponseWriter, r *http.Request, vm playerVM) {
	ctx, tc, player := r.Context(), vm.tallyCtx, vm.Player
	vm.Counter = tc.Tally.Preset().IsCounter()
	var err error
	if vm.Periods, err = a.store.ListPeriods(ctx, tc.Tally.ID); err != nil {
		internalError(w, "list periods", err)
		return
	}
	for i := range vm.Periods {
		if vm.Periods[i].ID == r.URL.Query().Get("season") {
			vm.Period = &vm.Periods[i]
		}
	}
	if vm.Period == nil {
		if vm.Period, err = a.store.CurrentPeriod(ctx, tc.Tally.ID); err != nil && !isNotFound(err) {
			internalError(w, "current period", err)
			return
		}
	}
	if vm.Period == nil {
		a.render(w, r, "player", vm)
		return
	}

	matches, err := a.store.PlayerMatches(ctx, tc.Tally.ID, vm.Period.ID, player.ID)
	if err != nil {
		internalError(w, "player matches", err)
		return
	}
	if vm.Awards, err = a.store.PlayerAwards(ctx, vm.Period.ID, player.ID); err != nil {
		internalError(w, "player awards", err)
		return
	}
	rounds, err := a.store.RoundsForPeriod(ctx, vm.Period.ID) // newest first
	if err != nil {
		internalError(w, "list rounds", err)
		return
	}

	dayPts := map[string]int{}
	dayMatches := map[string]int{}
	partners, opponents := map[string]*pairStat{}, map[string]*pairStat{}
	for _, m := range matches {
		mine := 0
		for s, side := range m.Sides {
			for _, p := range side.Players {
				if p.ID == player.ID {
					mine = s
				}
			}
		}
		me, them := m.Sides[mine], m.Sides[1-mine]
		switch me.Result {
		case ResultWin:
			vm.Won++
		case ResultDraw:
			vm.Drawn++
		default:
			vm.Lost++
		}
		dayPts[m.RoundID] += me.Points
		dayMatches[m.RoundID]++
		count := func(into map[string]*pairStat, p Participant) {
			ps := into[p.ID]
			if ps == nil {
				ps = &pairStat{ID: p.ID, Name: p.Name}
				into[p.ID] = ps
			}
			ps.Played++
			if me.Result == ResultWin {
				ps.Won++
			}
		}
		for _, p := range me.Players {
			if p.ID != player.ID {
				count(partners, p)
			}
		}
		for _, p := range them.Players {
			count(opponents, p)
		}
	}
	extras := vm.Awards[:0]
	for _, aw := range vm.Awards {
		dayPts[aw.RoundID] += aw.Points
		if aw.Kind == AwardScore {
			dayMatches[aw.RoundID]++
			vm.Scores++
		} else {
			extras = append(extras, aw)
		}
	}
	vm.Awards = extras
	vm.Partners, vm.Opponents = sortPairs(partners), sortPairs(opponents)
	slices.Reverse(matches)
	vm.Matches = matches

	// Rank after each play day the player took part in, plus the current rank.
	best := 0
	for _, rd := range rounds {
		if rd.Awards == 0 {
			continue
		}
		pts, played := dayPts[rd.ID], dayMatches[rd.ID]
		_, took := dayPts[rd.ID]
		if !took {
			continue
		}
		st, err := a.store.StandingsAsOf(ctx, vm.Period.ID, rd.PlayedOn)
		if err != nil {
			internalError(w, "standings", err)
			return
		}
		sortStandings(st)
		day := dayStat{Round: rd, Points: pts, Matches: played}
		for i, s := range st {
			if s.ParticipantID == player.ID {
				day.Rank = i + 1
			}
		}
		vm.Days = append(vm.Days, day)
		best = max(best, pts)
	}
	for i := range vm.Days {
		if best > 0 && vm.Days[i].Points > 0 {
			vm.Days[i].BarPct = max(2, vm.Days[i].Points*100/best)
		}
	}

	all, err := a.store.StandingsAsOf(ctx, vm.Period.ID, time.Time{})
	if err != nil {
		internalError(w, "standings", err)
		return
	}
	if vm.Counter {
		useEntries(all)
	}
	sortStandings(all)
	vm.Of = len(all)
	for i, s := range all {
		if s.ParticipantID == player.ID {
			vm.Rank, vm.Standing = i+1, s
		}
	}
	a.render(w, r, "player", vm)
}
