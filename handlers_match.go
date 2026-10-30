package main

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// matchForm holds a match form's raw values so a failed submit re-renders as typed.
type matchForm struct {
	Action string      // form URL
	Sel    [2][]string // participant id per side and slot
	Scores [2][]string // score per side and game
}

func newMatchForm(action string, sideSize, games int) *matchForm {
	f := &matchForm{Action: action}
	for s := range 2 {
		f.Sel[s] = make([]string, sideSize)
		f.Scores[s] = make([]string, games)
	}
	return f
}

func formFromMatch(m *Match, action string, sideSize, games int) *matchForm {
	f := newMatchForm(action, sideSize, games)
	for s, side := range m.Sides {
		for i, p := range side.Players {
			if i < sideSize {
				f.Sel[s][i] = p.ID
			}
		}
		for g, v := range side.Games {
			if g < games {
				f.Scores[s][g] = strconv.Itoa(v)
			}
		}
	}
	return f
}

func readMatchForm(r *http.Request, action string, sideSize, games int) *matchForm {
	f := newMatchForm(action, sideSize, games)
	for s := range 2 {
		for i := range sideSize {
			f.Sel[s][i] = r.FormValue(fmt.Sprintf("p%d_%d", s, i))
		}
		for g := range games {
			f.Scores[s][g] = strings.TrimSpace(r.FormValue(fmt.Sprintf("s%d_%d", s, g)))
		}
	}
	return f
}

// parse validates players and scores; valid is the set of this tally's player ids.
func (f *matchForm) parse(r *http.Request, cfg ScoringConfig, valid map[string]bool) (MatchResult, string) {
	var res MatchResult
	seen := map[string]bool{}
	for s := range 2 {
		for _, id := range f.Sel[s] {
			switch {
			case id == "" || !valid[id]:
				return res, tr(r, "err.match_players")
			case seen[id]:
				return res, tr(r, "err.match_duplicate")
			}
			seen[id] = true
			res.Players[s] = append(res.Players[s], id)
		}
		for _, v := range f.Scores[s] {
			n, err := strconv.Atoi(v)
			if err != nil {
				return res, tr(r, "err.match_scores")
			}
			res.Games[s] = append(res.Games[s], n)
		}
	}
	out, err := cfg.Score(res.Games[0], res.Games[1])
	if err != nil {
		return res, trErr(r, err)
	}
	res.Outcome = out
	return res, ""
}

// --- Start a play day ---

type playVM struct {
	tallyCtx
	Current  *Period
	Rounds   []Round
	Players  []Participant
	FDate    string
	FCourts  int
	FPresent map[string]bool
}

func (a *App) renderPlay(w http.ResponseWriter, r *http.Request, vm playVM, status int) {
	ctx := r.Context()
	cur, err := a.store.CurrentPeriod(ctx, vm.Tally.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		internalError(w, "current period", err)
		return
	}
	vm.Current = cur
	if cur != nil {
		if vm.Rounds, err = a.store.RoundsForPeriod(ctx, cur.ID); err != nil {
			internalError(w, "list rounds", err)
			return
		}
	}
	all, err := a.store.ListParticipants(ctx, vm.Tally.ID)
	if err != nil {
		internalError(w, "list players", err)
		return
	}
	for _, p := range all {
		if p.Active {
			vm.Players = append(vm.Players, p)
		}
	}
	if vm.FDate == "" {
		vm.FDate = today().Format(dateLayout)
	}
	if vm.FCourts == 0 {
		vm.FCourts = 1
		if len(vm.Rounds) > 0 {
			vm.FCourts = vm.Rounds[0].Courts
		}
	}
	a.renderStatus(w, r, "play", vm, status)
}

func (a *App) handlePlay(w http.ResponseWriter, r *http.Request) {
	tc, _, ok := a.adminTally(w, r, "play")
	if !ok {
		return
	}
	if r.URL.Query().Get("new") == "" {
		rd, err := a.store.RoundByDate(r.Context(), tc.Tally.ID, today())
		if err == nil {
			http.Redirect(w, r, tallyPath(tc.Tally, "/rounds/"+rd.ID), http.StatusSeeOther)
			return
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			internalError(w, "round by date", err)
			return
		}
	}
	a.renderPlay(w, r, playVM{tallyCtx: tc}, http.StatusOK)
}

func readCourts(r *http.Request) (int, bool) {
	n, err := strconv.Atoi(r.FormValue("courts"))
	return n, err == nil && n >= 1 && n <= 3
}

func presentSet(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func (a *App) handleCreateRound(w http.ResponseWriter, r *http.Request) {
	tc, u, ok := a.adminTally(w, r, "play")
	if !ok {
		return
	}
	limitBody(w, r, maxFormBytes)
	_ = r.ParseForm()
	present := r.Form["present"]
	courts, okC := readCourts(r)
	vm := playVM{tallyCtx: tc, FDate: r.FormValue("played_on"), FCourts: courts, FPresent: presentSet(present)}
	fail := func(key string) {
		vm.Err = tr(r, key)
		a.renderPlay(w, r, vm, http.StatusUnprocessableEntity)
	}
	d, okD := parseDate(vm.FDate)
	switch {
	case !okD:
		fail("err.date")
		return
	case !okC:
		fail("err.courts")
		return
	}
	ctx := r.Context()
	period, err := a.store.PeriodForDate(ctx, tc.Tally.ID, d)
	if errors.Is(err, pgx.ErrNoRows) {
		fail("err.no_period_for_date")
		return
	}
	if err != nil {
		internalError(w, "period for date", err)
		return
	}
	id, err := a.store.CreateRound(ctx, tc.Tally.ID, u.ID, period.ID, d, courts, present)
	if errors.Is(err, ErrRoundExists) {
		existing, err := a.store.RoundByDate(ctx, tc.Tally.ID, d)
		if err != nil {
			internalError(w, "round by date", err)
			return
		}
		id = existing.ID
	} else if err != nil {
		internalError(w, "create round", err)
		return
	} else {
		recordPlayDayStarted(ctx)
	}
	http.Redirect(w, r, tallyPath(tc.Tally, "/rounds/"+id), http.StatusSeeOther)
}

// --- Round (play day) page ---

type roundVM struct {
	tallyCtx
	Round      *Round
	Period     *Period
	SideSize   int
	Games      int
	Players    []Participant // every player, for the attendance list and selects
	Options    []Participant // what the player selects offer
	Present    map[string]bool
	PresentN   int
	OnCourt    []*Match
	Done       []*Match
	Forms      map[string]*matchForm // by match id, plus "new"
	FreeCourts int
	Waiting    []Participant // present, not on court; next shuffle picks from the front
	Played     map[string]int
	NeedPerCt  int
	CanShuffle bool
	AutoOpen   string // id of a form to show expanded after a failed submit
	Counter    bool   // counter tally: no matches, only point entries
	Awards     []Award
	EntryFor   []Participant // players offered in the points-entry grid
	FReason    string
	FPoints    map[string]string

	// Poin tab (RFC-001): each player's points per play, without match details.
	Tabs       bool   // show the Poin / Pertandingan tabs
	Tab        string // tabPoints or tabMatches
	ScoreMax   int
	MaxPlays   int
	ScoreRows  []scoreRow
	ScorePlays int
	PickNames  []string // what the player field suggests
	FPlayer    string
	FScore     string
	ConfirmNew string // typed name that isn't a player yet; asks before adding
	Focus      string // "player" or "points": where the cursor goes
}

// maxPlaysPerDay caps how often one player plays on a play day, like the
// club's sheet with its 7 slots (RFC-001 §6). Scores and matches both count.
const maxPlaysPerDay = 7

const (
	tabPoints   = "poin"
	tabMatches  = "match"
	tabCookie   = "round_tab"
	maxScoreLen = 4 // digits accepted in the points field
)

// playChip is one play in a player's row on the Poin tab.
type playChip struct {
	Points  int
	AwardID string // set for a score entry, which can be deleted here
	MatchID string // set for a play from a match, changed on the Pertandingan tab
	OnCourt bool   // a match that has no score yet
}

// scoreRow is one player's plays on a play day.
type scoreRow struct {
	Player Participant
	Chips  []playChip
	Total  int
	CanAdd bool
}

type roundData struct {
	tc      tallyCtx
	round   *Round
	period  *Period
	players []Participant
	present []string
	matches []*Match
	awards  []Award // extras
	scores  []Award // points entered without match details
}

func (a *App) loadRound(w http.ResponseWriter, r *http.Request, tc tallyCtx, roundID string) (*roundData, bool) {
	ctx := r.Context()
	rd, err := a.store.RoundByID(ctx, tc.Tally.ID, roundID)
	if isNotFound(err) {
		a.notFound(w, r)
		return nil, false
	}
	if err != nil {
		internalError(w, "load round", err)
		return nil, false
	}
	d := &roundData{tc: tc, round: rd}
	if d.period, err = a.store.PeriodByID(ctx, tc.Tally.ID, rd.PeriodID); err != nil {
		internalError(w, "load period", err)
		return nil, false
	}
	if d.players, err = a.store.ListParticipants(ctx, tc.Tally.ID); err != nil {
		internalError(w, "list players", err)
		return nil, false
	}
	if d.present, err = a.store.Attendance(ctx, rd.ID); err != nil {
		internalError(w, "attendance", err)
		return nil, false
	}
	if d.matches, err = a.store.MatchesForRound(ctx, tc.Tally.ID, rd.ID); err != nil {
		internalError(w, "list matches", err)
		return nil, false
	}
	if d.awards, err = a.store.ManualAwards(ctx, rd.ID); err != nil {
		internalError(w, "list awards", err)
		return nil, false
	}
	if d.scores, err = a.store.ScoreAwards(ctx, rd.ID); err != nil {
		internalError(w, "list scores", err)
		return nil, false
	}
	return d, true
}

// scoreMax is the most points one play can earn when points are entered per
// player, and false when the season's format doesn't support that: only a
// race to a target (badminton to 42) does for now.
func (d *roundData) scoreMax() (int, bool) {
	c := d.period.Scoring
	if d.tc.Tally.Preset().IsCounter() || c.Format != FormatContinuous {
		return 0, false
	}
	return c.Target + c.WinBonus, true
}

// scoreRows lists everyone who played today, by name: their scores in the
// order entered, then their matches.
func (d *roundData) scoreRows() (rows []scoreRow, plays int) {
	byID := map[string]*scoreRow{}
	row := func(id string) *scoreRow {
		if byID[id] == nil {
			byID[id] = &scoreRow{}
		}
		return byID[id]
	}
	for _, s := range d.scores {
		r := row(s.ParticipantID)
		r.Chips = append(r.Chips, playChip{Points: s.Points, AwardID: s.ID})
	}
	for _, m := range d.matches {
		for _, side := range m.Sides {
			for _, p := range side.Players {
				r := row(p.ID)
				r.Chips = append(r.Chips, playChip{Points: side.Points, MatchID: m.ID, OnCourt: m.Scheduled()})
			}
		}
	}
	for _, p := range d.players {
		r := byID[p.ID]
		if r == nil {
			continue
		}
		r.Player = p
		for _, c := range r.Chips {
			if !c.OnCourt {
				r.Total += c.Points
			}
		}
		r.CanAdd = len(r.Chips) < maxPlaysPerDay
		plays += len(r.Chips)
		rows = append(rows, *r)
	}
	slices.SortStableFunc(rows, func(a, b scoreRow) int {
		return strings.Compare(strings.ToLower(a.Player.Name), strings.ToLower(b.Player.Name))
	})
	return rows, plays
}

// roundTab is the tab asked for in ?tab=, remembered in a cookie; otherwise
// the last one used, Poin by default.
func roundTab(w http.ResponseWriter, r *http.Request) string {
	tab := r.URL.Query().Get("tab")
	if tab == tabPoints || tab == tabMatches {
		http.SetCookie(w, &http.Cookie{
			Name: tabCookie, Value: tab, Path: "/t/", MaxAge: 365 * 24 * 3600,
			HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: isHTTPS(r),
		})
		return tab
	}
	if c, err := r.Cookie(tabCookie); err == nil && c.Value == tabMatches {
		return tabMatches
	}
	return tabPoints
}

func (d *roundData) validPlayers() map[string]bool {
	m := map[string]bool{}
	for _, p := range d.players {
		m[p.ID] = true
	}
	return m
}

func (d *roundData) shuffleInput(sideSize int) shuffleInput {
	in := shuffleInput{PlayedToday: map[string]int{}, LastPlayed: map[string]time.Time{}, SideSize: sideSize, Courts: d.round.Courts}
	busy := map[string]bool{}
	for _, m := range d.matches {
		if m.Scheduled() {
			in.Courts--
		}
		for _, id := range m.PlayerIDs() {
			in.PlayedToday[id]++
			if m.CreatedAt.After(in.LastPlayed[id]) {
				in.LastPlayed[id] = m.CreatedAt
			}
			if m.Scheduled() {
				busy[id] = true
			}
		}
	}
	in.Courts = max(in.Courts, 0)
	active := map[string]bool{}
	for _, p := range d.players {
		active[p.ID] = p.Active
	}
	for _, id := range d.present {
		if active[id] && !busy[id] {
			in.Available = append(in.Available, id)
		}
	}
	slices.Sort(in.Available)
	return in
}

func (a *App) renderRound(w http.ResponseWriter, r *http.Request, d *roundData, override map[string]*matchForm, status int) {
	a.renderRoundVM(w, r, d, override, nil, status)
}

// renderRoundVM renders the play day; tweak (if set) adjusts the view model
// last, e.g. to keep a failed points entry as typed.
func (a *App) renderRoundVM(w http.ResponseWriter, r *http.Request, d *roundData, override map[string]*matchForm, tweak func(*roundVM), status int) {
	t := d.tc.Tally
	sideSize, games := t.SideSize(), d.period.Scoring.GamesPerMatch()
	vm := roundVM{
		tallyCtx: d.tc, Round: d.round, Period: d.period, SideSize: sideSize, Games: games,
		Players: d.players, Present: presentSet(d.present), Forms: map[string]*matchForm{},
		NeedPerCt: 2 * sideSize, Counter: t.Preset().IsCounter(), Awards: d.awards,
		MaxPlays: maxPlaysPerDay,
	}
	if vm.ScoreMax, vm.Tabs = d.scoreMax(); vm.Tabs {
		vm.Tab = roundTab(w, r)
		if len(override) > 0 {
			vm.Tab = tabMatches // a match form failed
		}
		vm.ScoreRows, vm.ScorePlays = d.scoreRows()
		for _, p := range d.players {
			if p.Active {
				vm.PickNames = append(vm.PickNames, p.Name)
			}
		}
		q := r.URL.Query()
		if r.Method == http.MethodGet && vm.Tab == tabPoints {
			if name := q.Get("player"); name != "" && !tooLong(name, maxPlayerNameLen) {
				vm.FPlayer, vm.Focus = name, "points"
			} else if q.Get("ok") == "score_saved" {
				vm.Focus = "player"
			}
		}
	}
	inMatch := map[string]bool{}
	for _, m := range d.matches {
		if m.Scheduled() {
			vm.OnCourt = append(vm.OnCourt, m)
			vm.Forms[m.ID] = formFromMatch(m, tallyPath(t, "/matches/"+m.ID), sideSize, games)
		} else {
			vm.Done = append(vm.Done, m)
		}
		for _, id := range m.PlayerIDs() {
			inMatch[id] = true
		}
	}
	slices.Reverse(vm.Done) // newest first
	vm.Forms["new"] = newMatchForm(tallyPath(t, "/rounds/"+d.round.ID+"/matches"), sideSize, games)
	for k, f := range override {
		vm.Forms[k] = f
		vm.AutoOpen = k
	}
	// The points grid lists who's here, or every active player if nobody is ticked.
	for _, p := range d.players {
		if vm.Present[p.ID] {
			vm.EntryFor = append(vm.EntryFor, p)
		}
	}
	if len(vm.EntryFor) == 0 {
		for _, p := range d.players {
			if p.Active {
				vm.EntryFor = append(vm.EntryFor, p)
			}
		}
	}
	for _, p := range d.players {
		if vm.Present[p.ID] {
			vm.PresentN++
		}
		// Selects offer active players plus anyone already in a match today.
		if p.Active || inMatch[p.ID] {
			vm.Options = append(vm.Options, p)
		}
	}

	in := d.shuffleInput(sideSize)
	vm.Played = in.PlayedToday
	vm.FreeCourts = in.Courts
	vm.CanShuffle = in.Courts > 0 && len(in.Available) >= vm.NeedPerCt
	byID := map[string]Participant{}
	for _, p := range d.players {
		byID[p.ID] = p
	}
	order := slices.Clone(in.Available)
	slices.SortStableFunc(order, func(a, b string) int {
		if d := in.PlayedToday[a] - in.PlayedToday[b]; d != 0 {
			return d
		}
		if c := in.LastPlayed[a].Compare(in.LastPlayed[b]); c != 0 {
			return c
		}
		return strings.Compare(strings.ToLower(byID[a].Name), strings.ToLower(byID[b].Name))
	})
	for _, id := range order {
		vm.Waiting = append(vm.Waiting, byID[id])
	}
	if tweak != nil {
		tweak(&vm)
	}
	a.renderStatus(w, r, "round", vm, status)
}

func (a *App) handleRound(w http.ResponseWriter, r *http.Request) {
	tc, _, ok := a.adminTally(w, r, "play")
	if !ok {
		return
	}
	d, ok := a.loadRound(w, r, tc, r.PathValue("rid"))
	if !ok {
		return
	}
	a.renderRound(w, r, d, nil, http.StatusOK)
}

func roundPath(t *Tally, roundID string) string { return tallyPath(t, "/rounds/"+roundID) }

func (a *App) handleRoundSetup(w http.ResponseWriter, r *http.Request) {
	tc, u, ok := a.adminTally(w, r, "play")
	if !ok {
		return
	}
	limitBody(w, r, maxFormBytes)
	d, ok := a.loadRound(w, r, tc, r.PathValue("rid"))
	if !ok {
		return
	}
	_ = r.ParseForm()
	courts, okC := readCourts(r)
	if !okC {
		d.tc.Err = tr(r, "err.courts")
		a.renderRound(w, r, d, nil, http.StatusUnprocessableEntity)
		return
	}
	if err := a.store.UpdateRoundSetup(r.Context(), tc.Tally.ID, u.ID, d.round, courts, r.Form["present"]); err != nil {
		internalError(w, "update round", err)
		return
	}
	redirectOK(w, r, roundPath(tc.Tally, d.round.ID), "round_saved")
}

func (a *App) handleShuffle(w http.ResponseWriter, r *http.Request) {
	tc, u, ok := a.adminTally(w, r, "play")
	if !ok {
		return
	}
	limitBody(w, r, maxFormBytes)
	d, ok := a.loadRound(w, r, tc, r.PathValue("rid"))
	if !ok {
		return
	}
	sideSize := tc.Tally.SideSize()
	in := d.shuffleInput(sideSize)
	if in.Courts == 0 {
		d.tc.Err = tr(r, "err.courts_busy")
		a.renderRound(w, r, d, nil, http.StatusUnprocessableEntity)
		return
	}
	groups, _ := suggestMatches(in, shuffleRNG())
	if len(groups) == 0 {
		d.tc.Err = tr(r, "err.not_enough_players", 2*sideSize)
		a.renderRound(w, r, d, nil, http.StatusUnprocessableEntity)
		return
	}
	if err := a.store.ScheduleMatches(r.Context(), tc.Tally.ID, u.ID, d.round, groups); err != nil {
		internalError(w, "schedule matches", err)
		return
	}
	recordMatchEvent(r.Context(), "scheduled", len(groups))
	http.Redirect(w, r, roundPath(tc.Tally, d.round.ID)+"#court", http.StatusSeeOther)
}

// handleAddMatch records an already-played match picked by hand.
func (a *App) handleAddMatch(w http.ResponseWriter, r *http.Request) {
	tc, u, ok := a.adminTally(w, r, "play")
	if !ok {
		return
	}
	limitBody(w, r, maxFormBytes)
	d, ok := a.loadRound(w, r, tc, r.PathValue("rid"))
	if !ok {
		return
	}
	done, ok := a.claimSubmit(w, r, roundPath(tc.Tally, d.round.ID)+"?ok=match_saved#done")
	if !ok {
		return
	}
	saved := false
	defer done(&saved)
	f := readMatchForm(r, tallyPath(tc.Tally, "/rounds/"+d.round.ID+"/matches"), tc.Tally.SideSize(), d.period.Scoring.GamesPerMatch())
	res, msg := f.parse(r, d.period.Scoring, d.validPlayers())
	if msg != "" {
		d.tc.Err = msg
		a.renderRound(w, r, d, map[string]*matchForm{"new": f}, http.StatusUnprocessableEntity)
		return
	}
	if _, err := a.store.SaveMatch(r.Context(), tc.Tally.ID, u.ID, d.round, nil, res); err != nil {
		internalError(w, "save match", err)
		return
	}
	saved = true
	recordMatchEvent(r.Context(), "added", 1)
	http.Redirect(w, r, roundPath(tc.Tally, d.round.ID)+"?ok=match_saved#done", http.StatusSeeOther)
}

// --- A single match: score, edit, delete ---

func (a *App) loadMatch(w http.ResponseWriter, r *http.Request) (tallyCtx, *User, *roundData, *Match, bool) {
	tc, u, ok := a.adminTally(w, r, "play")
	if !ok {
		return tc, nil, nil, nil, false
	}
	m, err := a.store.MatchByID(r.Context(), tc.Tally.ID, r.PathValue("mid"))
	if isNotFound(err) {
		a.notFound(w, r)
		return tc, nil, nil, nil, false
	}
	if err != nil {
		internalError(w, "load match", err)
		return tc, nil, nil, nil, false
	}
	d, ok := a.loadRound(w, r, tc, m.RoundID)
	return tc, u, d, m, ok
}

type matchEditVM struct {
	tallyCtx
	Round    *Round
	Period   *Period
	Match    *Match
	Form     *matchForm
	Options  []Participant
	SideSize int
	Games    int
}

func (a *App) renderMatchEdit(w http.ResponseWriter, r *http.Request, d *roundData, m *Match, f *matchForm, status int) {
	sideSize, games := d.tc.Tally.SideSize(), d.period.Scoring.GamesPerMatch()
	if f == nil {
		f = formFromMatch(m, tallyPath(d.tc.Tally, "/matches/"+m.ID), sideSize, games)
	}
	inMatch := presentSet(m.PlayerIDs())
	var opts []Participant
	for _, p := range d.players {
		if p.Active || inMatch[p.ID] {
			opts = append(opts, p)
		}
	}
	a.renderStatus(w, r, "match_edit", matchEditVM{
		tallyCtx: d.tc, Round: d.round, Period: d.period, Match: m, Form: f,
		Options: opts, SideSize: sideSize, Games: games,
	}, status)
}

func (a *App) handleEditMatchForm(w http.ResponseWriter, r *http.Request) {
	_, _, d, m, ok := a.loadMatch(w, r)
	if !ok {
		return
	}
	a.renderMatchEdit(w, r, d, m, nil, http.StatusOK)
}

func (a *App) handleMatchAction(w http.ResponseWriter, r *http.Request) {
	tc, u, d, m, ok := a.loadMatch(w, r)
	if !ok {
		return
	}
	limitBody(w, r, maxFormBytes)
	back := roundPath(tc.Tally, d.round.ID)
	switch r.FormValue("action") {
	case "delete":
		if err := a.store.DeleteMatch(r.Context(), tc.Tally.ID, u.ID, m); err != nil {
			internalError(w, "delete match", err)
			return
		}
		key, action := "match_deleted", "deleted"
		if m.Scheduled() {
			key, action = "match_cancelled", "cancelled"
		}
		recordMatchEvent(r.Context(), action, 1)
		redirectOK(w, r, back, key)
	case "save":
		f := readMatchForm(r, tallyPath(tc.Tally, "/matches/"+m.ID), tc.Tally.SideSize(), d.period.Scoring.GamesPerMatch())
		res, msg := f.parse(r, d.period.Scoring, d.validPlayers())
		if msg != "" {
			d.tc.Err = msg
			if r.FormValue("from") == "edit" {
				a.renderMatchEdit(w, r, d, m, f, http.StatusUnprocessableEntity)
			} else {
				a.renderRound(w, r, d, map[string]*matchForm{m.ID: f}, http.StatusUnprocessableEntity)
			}
			return
		}
		if _, err := a.store.SaveMatch(r.Context(), tc.Tally.ID, u.ID, d.round, m, res); err != nil {
			internalError(w, "save match", err)
			return
		}
		action := "edited"
		if m.Scheduled() {
			action = "scored"
		}
		recordMatchEvent(r.Context(), action, 1)
		http.Redirect(w, r, back+"?ok=match_saved#done", http.StatusSeeOther)
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
	}
}

// --- Manual points ---

const (
	maxAwardPoints = 9999
	maxReasonLen   = 120
)

// handleAddAwards records points typed into the play day's grid: one number
// per player (blank = skip, negative = penalty) with a shared reason.
func (a *App) handleAddAwards(w http.ResponseWriter, r *http.Request) {
	tc, u, ok := a.adminTally(w, r, "play")
	if !ok {
		return
	}
	limitBody(w, r, maxFormBytes)
	d, ok := a.loadRound(w, r, tc, r.PathValue("rid"))
	if !ok {
		return
	}
	done, ok := a.claimSubmit(w, r, roundPath(tc.Tally, d.round.ID)+"?ok=awards_saved&tab=poin#awards")
	if !ok {
		return
	}
	saved := false
	defer done(&saved)
	reason := strings.Join(strings.Fields(r.FormValue("reason")), " ")
	typed := map[string]string{}
	var entries []AwardInput
	errKey := ""
	for _, p := range d.players {
		v := strings.TrimSpace(r.FormValue("pts_" + p.ID))
		if v == "" {
			continue
		}
		typed[p.ID] = v
		n, err := strconv.Atoi(strings.TrimPrefix(v, "+"))
		switch {
		case err != nil:
			errKey = "err.number"
		case n == 0:
			continue
		case n > maxAwardPoints || n < -maxAwardPoints:
			errKey = "err.award_range"
		}
		entries = append(entries, AwardInput{ParticipantID: p.ID, Points: n})
	}
	switch {
	case errKey == "" && len(entries) == 0:
		errKey = "err.award_empty"
	case errKey == "" && tooLong(reason, maxReasonLen):
		errKey = "err.reason_long"
	}
	if errKey != "" {
		if errKey == "err.award_range" {
			d.tc.Err = tr(r, errKey, maxAwardPoints)
		} else {
			d.tc.Err = tr(r, errKey)
		}
		a.renderRoundVM(w, r, d, nil, func(vm *roundVM) {
			vm.FPoints, vm.FReason, vm.AutoOpen, vm.Tab = typed, reason, "awards", tabPoints
		}, http.StatusUnprocessableEntity)
		return
	}
	if err := a.store.AddAwards(r.Context(), tc.Tally.ID, u.ID, d.round, entries, reason); err != nil {
		internalError(w, "add awards", err)
		return
	}
	saved = true
	recordAwards(r.Context(), len(entries))
	http.Redirect(w, r, roundPath(tc.Tally, d.round.ID)+"?ok=awards_saved&tab=poin#awards", http.StatusSeeOther)
}

func (a *App) handleDeleteAward(w http.ResponseWriter, r *http.Request) {
	tc, u, ok := a.adminTally(w, r, "play")
	if !ok {
		return
	}
	limitBody(w, r, maxFormBytes)
	roundID, kind, err := a.store.DeleteAward(r.Context(), tc.Tally.ID, u.ID, r.PathValue("aid"))
	if isNotFound(err) {
		a.notFound(w, r)
		return
	}
	if err != nil {
		internalError(w, "delete award", err)
		return
	}
	if kind == AwardScore {
		http.Redirect(w, r, roundPath(tc.Tally, roundID)+"?ok=score_deleted&tab=poin#today", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, roundPath(tc.Tally, roundID)+"?ok=award_deleted&tab=poin#awards", http.StatusSeeOther)
}

// handleAddScore records one play's points for a player typed or picked by
// name (RFC-001 Poin tab). A name that isn't a player yet is added only after
// the admin confirms (confirm_new=1).
func (a *App) handleAddScore(w http.ResponseWriter, r *http.Request) {
	tc, u, ok := a.adminTally(w, r, "play")
	if !ok {
		return
	}
	limitBody(w, r, maxFormBytes)
	d, ok := a.loadRound(w, r, tc, r.PathValue("rid"))
	if !ok {
		return
	}
	limit, ok := d.scoreMax()
	if !ok {
		a.notFound(w, r)
		return
	}
	back := roundPath(tc.Tally, d.round.ID) + "?ok=score_saved&tab=poin" // no #fragment: it would stop autofocus
	done, ok := a.claimSubmit(w, r, back)
	if !ok {
		return
	}
	saved := false
	defer done(&saved)

	name := strings.Join(strings.Fields(r.FormValue("player")), " ")
	typed := strings.TrimSpace(r.FormValue("points"))
	fail := func(status int, tweak func(*roundVM)) {
		a.renderRoundVM(w, r, d, nil, func(vm *roundVM) {
			vm.Tab, vm.FPlayer, vm.FScore = tabPoints, name, typed
			if tweak != nil {
				tweak(vm)
			}
		}, status)
	}
	points, err := strconv.Atoi(typed)
	switch {
	case name == "":
		d.tc.Err = tr(r, "err.score_player")
		fail(http.StatusUnprocessableEntity, func(vm *roundVM) { vm.Focus = "player" })
		return
	case tooLong(name, maxPlayerNameLen):
		d.tc.Err = tr(r, "err.player_name_long", name, maxPlayerNameLen)
		fail(http.StatusUnprocessableEntity, func(vm *roundVM) { vm.Focus = "player" })
		return
	case len(typed) > maxScoreLen || err != nil || points < 0 || points > limit:
		d.tc.Err = tr(r, "err.score_range", limit)
		fail(http.StatusUnprocessableEntity, func(vm *roundVM) { vm.Focus = "points" })
		return
	}

	var playerID, newName string
	for _, p := range d.players {
		if strings.EqualFold(p.Name, name) {
			playerID, name = p.ID, p.Name
		}
	}
	if playerID == "" {
		if r.FormValue("confirm_new") != "1" {
			fail(http.StatusOK, func(vm *roundVM) { vm.ConfirmNew = name })
			return
		}
		newName = name
	}
	switch err := a.store.AddScore(r.Context(), tc.Tally.ID, u.ID, d.round, playerID, newName, points, maxPlaysPerDay); {
	case errors.Is(err, ErrPlayCap):
		d.tc.Err = tr(r, "err.score_cap", name, maxPlaysPerDay)
		fail(http.StatusUnprocessableEntity, func(vm *roundVM) { vm.FPlayer, vm.Focus = "", "player" })
		return
	case errors.Is(err, ErrNameTaken):
		// Someone added that name since the page loaded; the admin can resend.
		d.tc.Err = tr(r, "err.score_retry")
		fail(http.StatusConflict, nil)
		return
	case err != nil:
		internalError(w, "add score", err)
		return
	}
	saved = true
	if newName != "" {
		recordPlayersAdded(r.Context(), 1)
	}
	recordScores(r.Context(), 1)
	http.Redirect(w, r, back, http.StatusSeeOther)
}
