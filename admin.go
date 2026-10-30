package main

import (
	"errors"
	"log"
	"net/http"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// requireAdmin lets the request through only if aio says, right now, that the
// user is an aio admin (RFC-002 in all-in-one). There is no cache: a user
// removed from aio's admin group loses access on their next request. Pages
// are 404 for everyone else so they aren't advertised; if aio can't be asked,
// the page is refused (fails closed). This is the site admin, unrelated to a
// tally's own admins.
func (a *App) requireAdmin(w http.ResponseWriter, r *http.Request) *User {
	if !a.aio.Enabled() {
		a.notFound(w, r)
		return nil
	}
	u := a.requireUser(w, r)
	if u == nil {
		return nil
	}
	if u.AioUserID == "" {
		recordAdminCheck(r.Context(), "local_account")
		a.notFound(w, r)
		return nil
	}
	st, err := a.aio.userStatus(r.Context(), u.AioUserID)
	if err != nil {
		log.Printf("admin: status check for %s: %v", u.Username, err)
		recordAdminCheck(r.Context(), "error")
		a.renderStatus(w, r, "adminerror", nil, http.StatusServiceUnavailable)
		return nil
	}
	if !st.Admin {
		recordAdminCheck(r.Context(), "not_admin")
		a.notFound(w, r)
		return nil
	}
	recordAdminCheck(r.Context(), "admin")
	w.Header().Set("Cache-Control", "no-store")
	return u
}

func (a *App) logAdminView(r *http.Request, admin *User, page, subjectID string) {
	if err := a.store.LogAdminView(r.Context(), admin.ID, page, subjectID); err != nil {
		log.Printf("admin: audit log (%s %s %s): %v", admin.Username, page, subjectID, err)
	}
}

type adminUsersVM struct {
	Users []AdminUserRow
}

func (a *App) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	admin := a.requireAdmin(w, r)
	if admin == nil {
		return
	}
	users, err := a.store.AdminUsers(r.Context())
	if err != nil {
		internalError(w, "admin: list users", err)
		return
	}
	a.logAdminView(r, admin, "users", "")
	a.render(w, r, "admin", adminUsersVM{Users: users})
}

// adminTally loads /admin/tallies/{id} for a site admin, private tallies
// included; the views are read-only and each one is logged.
func (a *App) siteAdminTally(w http.ResponseWriter, r *http.Request) (*User, tallyCtx, bool) {
	admin := a.requireAdmin(w, r)
	if admin == nil {
		return nil, tallyCtx{}, false
	}
	id := r.PathValue("id")
	if uuid.Validate(id) != nil {
		a.notFound(w, r)
		return nil, tallyCtx{}, false
	}
	t, err := a.store.TallyByID(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		a.notFound(w, r)
		return nil, tallyCtx{}, false
	}
	if err != nil {
		internalError(w, "admin: load tally", err)
		return nil, tallyCtx{}, false
	}
	return admin, tallyCtx{Tally: t}, true
}

func (a *App) logAdminTallyView(r *http.Request, admin *User, page, tallyID, detail string) {
	if err := a.store.LogAdminTallyView(r.Context(), admin.ID, page, tallyID, detail); err != nil {
		log.Printf("admin: audit log (%s %s %s %s): %v", admin.Username, page, tallyID, detail, err)
	}
}

type adminSeasonRow struct {
	Period   Period
	PlayDays int
	Players  int
	Plays    int
	Points   int
}

type adminTallyVM struct {
	tallyCtx
	Owner   string
	Admins  []Admin
	Seasons []adminSeasonRow
	Players []TallyPlayerRow
}

func (a *App) handleAdminTally(w http.ResponseWriter, r *http.Request) {
	admin, tc, ok := a.siteAdminTally(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	vm := adminTallyVM{tallyCtx: tc}
	var err error
	if vm.Admins, err = a.store.ListAdmins(ctx, tc.Tally.ID); err != nil {
		internalError(w, "admin: tally admins", err)
		return
	}
	periods, err := a.store.ListPeriods(ctx, tc.Tally.ID)
	if err != nil {
		internalError(w, "admin: periods", err)
		return
	}
	for _, p := range periods {
		row := adminSeasonRow{Period: p}
		rounds, err := a.store.RoundsForPeriod(ctx, p.ID)
		if err != nil {
			internalError(w, "admin: rounds", err)
			return
		}
		stats, players, err := a.store.SeasonDayStats(ctx, p.ID)
		if err != nil {
			internalError(w, "admin: season stats", err)
			return
		}
		row.PlayDays, row.Players = len(rounds), players
		for _, s := range stats {
			row.Plays += s.Plays
			row.Points += s.Points
		}
		vm.Seasons = append(vm.Seasons, row)
	}
	if vm.Players, err = a.store.TallyPlayers(ctx, tc.Tally.ID); err != nil {
		internalError(w, "admin: players", err)
		return
	}
	a.logAdminTallyView(r, admin, "tally", tc.Tally.ID, "")
	a.render(w, r, "admintally", vm)
}

func (a *App) handleAdminSeason(w http.ResponseWriter, r *http.Request) {
	admin, tc, ok := a.siteAdminTally(w, r)
	if !ok {
		return
	}
	p, err := a.store.PeriodByID(r.Context(), tc.Tally.ID, r.PathValue("pid"))
	if isNotFound(err) {
		a.notFound(w, r)
		return
	}
	if err != nil {
		internalError(w, "admin: load period", err)
		return
	}
	a.logAdminTallyView(r, admin, "season", tc.Tally.ID, p.ID)
	a.periodPage(w, r, periodVM{tallyCtx: tc, Period: p, Admin: true})
}

func (a *App) handleAdminPlayer(w http.ResponseWriter, r *http.Request) {
	admin, tc, ok := a.siteAdminTally(w, r)
	if !ok {
		return
	}
	player, err := a.store.ParticipantByID(r.Context(), tc.Tally.ID, r.PathValue("pid"))
	if isNotFound(err) {
		a.notFound(w, r)
		return
	}
	if err != nil {
		internalError(w, "admin: load player", err)
		return
	}
	a.logAdminTallyView(r, admin, "player", tc.Tally.ID, player.ID)
	a.playerPage(w, r, playerVM{tallyCtx: tc, Player: player, Admin: true})
}

type adminDayVM struct {
	tallyCtx
	Round     *Round
	Period    *Period
	ScoreRows []scoreRow
	Plays     int
	OnCourt   []*Match
	Done      []*Match
	Awards    []Award
	Present   []Participant
}

// PlayerHref links a player inside the site admin's view.
func (v adminDayVM) PlayerHref(id string) string {
	return "/admin/tallies/" + v.Tally.ID + "/players/" + id + "?season=" + v.Period.ID
}

func (a *App) handleAdminDay(w http.ResponseWriter, r *http.Request) {
	admin, tc, ok := a.siteAdminTally(w, r)
	if !ok {
		return
	}
	d, ok := a.loadRound(w, r, tc, r.PathValue("rid"))
	if !ok {
		return
	}
	vm := adminDayVM{tallyCtx: tc, Round: d.round, Period: d.period, Awards: d.awards}
	vm.ScoreRows, vm.Plays = d.scoreRows()
	for _, m := range d.matches {
		if m.Scheduled() {
			vm.OnCourt = append(vm.OnCourt, m)
		} else {
			vm.Done = append(vm.Done, m)
		}
	}
	slices.Reverse(vm.Done)
	present := presentSet(d.present)
	for _, p := range d.players {
		if present[p.ID] {
			vm.Present = append(vm.Present, p)
		}
	}
	a.logAdminTallyView(r, admin, "day", tc.Tally.ID, d.round.ID)
	a.render(w, r, "adminday", vm)
}

type adminUserVM struct {
	User    *User
	Tallies []AdminTallyRow
}

func (a *App) handleAdminUser(w http.ResponseWriter, r *http.Request) {
	admin := a.requireAdmin(w, r)
	if admin == nil {
		return
	}
	id := r.PathValue("id")
	if uuid.Validate(id) != nil {
		a.notFound(w, r)
		return
	}
	u, err := a.store.UserByID(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		a.notFound(w, r)
		return
	}
	if err != nil {
		internalError(w, "admin: load user", err)
		return
	}
	tallies, err := a.store.AdminTalliesOf(r.Context(), u.ID)
	if err != nil {
		internalError(w, "admin: tallies of user", err)
		return
	}
	a.logAdminView(r, admin, "user", u.ID)
	a.render(w, r, "adminuser", adminUserVM{User: u, Tallies: tallies})
}
