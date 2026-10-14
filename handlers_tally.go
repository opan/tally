package main

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

const (
	maxTallyNameLen  = 80
	maxTallyDescLen  = 500
	maxPlayerNameLen = 40
	maxPlayersPerAdd = 200
	maxPeriodNameLen = 60
)

func tooLong(s string, max int) bool { return utf8.RuneCountInString(s) > max }

func tr(r *http.Request, key string, args ...any) string {
	return translate(requestLang(r), key, args...)
}

// trErr localizes a ScoreError, or falls back to treating err's text as a key.
func trErr(r *http.Request, err error) string {
	var se *ScoreError
	if errors.As(err, &se) {
		return tr(r, se.Key, se.Args...)
	}
	return tr(r, err.Error())
}

// requireUser returns the logged-in user, or redirects to login and returns nil.
func (a *App) requireUser(w http.ResponseWriter, r *http.Request) *User {
	u := currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
	}
	return u
}

func baseURL(r *http.Request) string {
	if b := strings.TrimRight(env("BASE_URL", ""), "/"); b != "" {
		return b
	}
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// tallyCtx is the shared part of every /t/{slug} page's view model.
type tallyCtx struct {
	Tally  *Tally
	Role   string
	Tab    string
	Notice string
	Err    string
}

func (c tallyCtx) IsAdmin() bool { return c.Role != "" }
func (c tallyCtx) IsOwner() bool { return c.Role == RoleOwner }

// loadTally resolves /t/{slug} and the viewer's role. Private tallies look
// like 404s to non-admins. ok is false once a response has been written.
func (a *App) loadTally(w http.ResponseWriter, r *http.Request, tab string) (tc tallyCtx, ok bool) {
	t, err := a.store.TallyBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, pgx.ErrNoRows) {
		a.notFound(w, r)
		return tc, false
	}
	if err != nil {
		internalError(w, "load tally", err)
		return tc, false
	}
	tc = tallyCtx{Tally: t, Tab: tab, Notice: okNotice(r)}
	if u := currentUser(r); u != nil {
		if tc.Role, err = a.store.RoleFor(r.Context(), t.ID, u.ID); err != nil {
			internalError(w, "load role", err)
			return tc, false
		}
	}
	if t.Visibility == VisibilityPrivate && !tc.IsAdmin() {
		a.notFound(w, r)
		return tc, false
	}
	return tc, true
}

// adminTally is loadTally for pages only admins may use.
func (a *App) adminTally(w http.ResponseWriter, r *http.Request, tab string) (tallyCtx, *User, bool) {
	u := a.requireUser(w, r)
	if u == nil {
		return tallyCtx{}, nil, false
	}
	tc, ok := a.loadTally(w, r, tab)
	if !ok {
		return tc, nil, false
	}
	if !tc.IsAdmin() {
		a.notFound(w, r)
		return tc, nil, false
	}
	return tc, u, true
}

// okNotices are the success messages a redirect may ask for with ?ok=.
var okNotices = map[string]bool{
	"tally_created": true, "settings_saved": true, "admin_added": true, "admin_removed": true,
	"branding_saved": true, "player_renamed": true, "player_activated": true, "player_deactivated": true, "player_deleted": true,
	"period_created": true, "period_saved": true,
	"round_saved": true, "awards_saved": true, "award_deleted": true, "match_saved": true, "match_deleted": true, "match_cancelled": true,
}

func okNotice(r *http.Request) string {
	if k := r.URL.Query().Get("ok"); okNotices[k] {
		return tr(r, "ok."+k)
	}
	return ""
}

func redirectOK(w http.ResponseWriter, r *http.Request, path, key string) {
	http.Redirect(w, r, path+"?ok="+key, http.StatusSeeOther)
}

func tallyPath(t *Tally, sub string) string { return "/t/" + t.Slug + sub }

// --- Dashboard & create ---

type dashboardVM struct {
	Cards     []TallyCard
	Presets   []Preset
	Err       string
	FName     string
	FSlug     string
	FDesc     string
	FTemplate string
	FSideSize string
}

func (a *App) renderDashboard(w http.ResponseWriter, r *http.Request, vm dashboardVM, status int) {
	cards, err := a.store.TalliesForUser(r.Context(), currentUser(r).ID)
	if err != nil {
		internalError(w, "list tallies", err)
		return
	}
	vm.Cards = cards
	vm.Presets = presets
	if vm.FTemplate == "" {
		vm.FTemplate = presets[0].ID
	}
	if vm.FSideSize == "" {
		vm.FSideSize = "5"
	}
	a.renderStatus(w, r, "dashboard", vm, status)
}

func (a *App) handleCreateTally(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	limitBody(w, r, maxFormBytes)
	name := strings.TrimSpace(r.FormValue("name"))
	desc := strings.TrimSpace(r.FormValue("description"))
	tmpl := r.FormValue("template")
	slug := normalizeSlug(r.FormValue("slug"))
	if slug == "" {
		slug = normalizeSlug(name)
	}
	fail := func(key string, args ...any) {
		a.renderDashboard(w, r, dashboardVM{Err: tr(r, key, args...), FName: name, FSlug: r.FormValue("slug"), FDesc: desc, FTemplate: tmpl, FSideSize: r.FormValue("side_size")}, http.StatusUnprocessableEntity)
	}
	switch {
	case name == "":
		fail("err.name_required")
		return
	case tooLong(name, maxTallyNameLen):
		fail("err.name_long", maxTallyNameLen)
		return
	case tooLong(desc, maxTallyDescLen):
		fail("err.desc_long", maxTallyDescLen)
		return
	}
	if key := validateSlug(slug); key != "" {
		fail(key)
		return
	}
	if _, ok := presetByID(tmpl); !ok {
		fail("err.template")
		return
	}

	preset, _ := presetByID(tmpl)
	var sideSize *int
	if len(preset.TeamSizes) > 0 {
		n, err := strconv.Atoi(r.FormValue("side_size"))
		if err != nil || !preset.AllowsTeamSize(n) {
			fail("err.team_size")
			return
		}
		sideSize = &n
	}
	t, err := a.store.CreateTally(r.Context(), u.ID, Tally{Slug: slug, Name: name, Description: desc, Template: tmpl, Visibility: VisibilityLink, SideSizeSet: sideSize})
	if errors.Is(err, ErrSlugTaken) {
		fail("err.slug_taken")
		return
	}
	if err != nil {
		internalError(w, "create tally", err)
		return
	}
	recordTallyCreated(r.Context(), tmpl)
	redirectOK(w, r, tallyPath(t, "/players"), "tally_created")
}

// --- Players ---

type playersVM struct {
	tallyCtx
	Players     []Participant
	Active      int
	Added       []string
	Skipped     []string
	FNames      string
	JustCreated bool
}

func (a *App) renderPlayers(w http.ResponseWriter, r *http.Request, vm playersVM, status int) {
	players, err := a.store.ListParticipants(r.Context(), vm.Tally.ID)
	if err != nil {
		internalError(w, "list players", err)
		return
	}
	vm.Players = players
	for _, p := range players {
		if p.Active {
			vm.Active++
		}
	}
	vm.JustCreated = r.URL.Query().Get("ok") == "tally_created"
	a.renderStatus(w, r, "players", vm, status)
}

func (a *App) handlePlayers(w http.ResponseWriter, r *http.Request) {
	tc, _, ok := a.adminTally(w, r, "players")
	if !ok {
		return
	}
	a.renderPlayers(w, r, playersVM{tallyCtx: tc}, http.StatusOK)
}

var listMarkerRe = regexp.MustCompile(`^\s*(?:\d+\s*[.)]|[-*•])\s*`)

// splitNames turns a pasted list into clean, unique names. It accepts one name
// per line (or comma-separated) and strips list markers like "1." or "-",
// so a roll call copied from a chat works as-is.
func splitNames(raw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' }) {
		name := strings.Join(strings.Fields(listMarkerRe.ReplaceAllString(line, "")), " ")
		key := strings.ToLower(name)
		if name == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, name)
	}
	return out
}

func (a *App) handleAddPlayers(w http.ResponseWriter, r *http.Request) {
	tc, u, ok := a.adminTally(w, r, "players")
	if !ok {
		return
	}
	limitBody(w, r, maxFormBytes)
	raw := r.FormValue("names")
	names := splitNames(raw)
	fail := func(key string, args ...any) {
		tc.Err = tr(r, key, args...)
		a.renderPlayers(w, r, playersVM{tallyCtx: tc, FNames: raw}, http.StatusUnprocessableEntity)
	}
	switch {
	case len(names) == 0:
		fail("err.players_required")
		return
	case len(names) > maxPlayersPerAdd:
		fail("err.players_too_many", maxPlayersPerAdd)
		return
	}
	for _, n := range names {
		if tooLong(n, maxPlayerNameLen) {
			fail("err.player_name_long", n, maxPlayerNameLen)
			return
		}
	}
	added, skipped, err := a.store.AddParticipants(r.Context(), tc.Tally.ID, u.ID, names)
	if err != nil {
		internalError(w, "add players", err)
		return
	}
	recordPlayersAdded(r.Context(), len(added))
	a.renderPlayers(w, r, playersVM{tallyCtx: tc, Added: added, Skipped: skipped}, http.StatusOK)
}

func (a *App) handlePlayerAction(w http.ResponseWriter, r *http.Request) {
	tc, u, ok := a.adminTally(w, r, "players")
	if !ok {
		return
	}
	limitBody(w, r, maxFormBytes)
	ctx, id, back := r.Context(), r.PathValue("id"), tallyPath(tc.Tally, "/players")
	fail := func(key string, args ...any) {
		tc.Err = tr(r, key, args...)
		a.renderPlayers(w, r, playersVM{tallyCtx: tc}, http.StatusUnprocessableEntity)
	}

	var err error
	var okKey string
	switch r.FormValue("action") {
	case "rename":
		name := strings.Join(strings.Fields(r.FormValue("name")), " ")
		if name == "" {
			fail("err.players_required")
			return
		}
		if tooLong(name, maxPlayerNameLen) {
			fail("err.player_name_long", name, maxPlayerNameLen)
			return
		}
		err, okKey = a.store.RenameParticipant(ctx, tc.Tally.ID, u.ID, id, name), "player_renamed"
	case "activate":
		err, okKey = a.store.SetParticipantActive(ctx, tc.Tally.ID, u.ID, id, true), "player_activated"
	case "deactivate":
		err, okKey = a.store.SetParticipantActive(ctx, tc.Tally.ID, u.ID, id, false), "player_deactivated"
	case "delete":
		err, okKey = a.store.DeleteParticipant(ctx, tc.Tally.ID, u.ID, id), "player_deleted"
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	switch {
	case isNotFound(err):
		a.notFound(w, r)
	case errors.Is(err, ErrNameTaken):
		fail("err.player_exists")
	case errors.Is(err, ErrInUse):
		fail("err.player_in_use")
	case err != nil:
		internalError(w, "player action", err)
	default:
		redirectOK(w, r, back, okKey)
	}
}

// --- Periods ---

// periodForm holds the raw form values so a failed submit re-renders as typed.
type periodForm struct {
	Name       string
	StartsOn   string
	EndsOn     string
	Format     string
	Target     string
	GameTarget string
	WinBonus   string
	WinPts     string
	DrawPts    string
	LossPts    string
}

func periodFormFrom(p Period) periodForm {
	f := periodForm{
		Name:     p.Name,
		StartsOn: p.StartsOn.Format(dateLayout),
		EndsOn:   p.EndsOn.Format(dateLayout),
		Format:   p.Scoring.Format,
		WinBonus: strconv.Itoa(p.Scoring.WinBonus),
	}
	// Prefill every format's fields so switching format keeps sane values.
	f.Target = strconv.Itoa(DefaultContinuous.Target)
	f.GameTarget = strconv.Itoa(DefaultReset.GameTarget)
	o := DefaultOutcome
	switch p.Scoring.Format {
	case FormatContinuous:
		f.Target = strconv.Itoa(p.Scoring.Target)
	case FormatReset:
		f.GameTarget = strconv.Itoa(p.Scoring.GameTarget)
	case FormatOutcome:
		o = p.Scoring
	}
	f.WinPts, f.DrawPts, f.LossPts = strconv.Itoa(o.WinPts), strconv.Itoa(o.DrawPts), strconv.Itoa(o.LossPts)
	return f
}

func readPeriodForm(r *http.Request) periodForm {
	return periodForm{
		Name:       strings.TrimSpace(r.FormValue("name")),
		StartsOn:   r.FormValue("starts_on"),
		EndsOn:     r.FormValue("ends_on"),
		Format:     r.FormValue("format"),
		Target:     strings.TrimSpace(r.FormValue("target")),
		GameTarget: strings.TrimSpace(r.FormValue("game_target")),
		WinBonus:   strings.TrimSpace(r.FormValue("win_bonus")),
		WinPts:     strings.TrimSpace(r.FormValue("win_pts")),
		DrawPts:    strings.TrimSpace(r.FormValue("draw_pts")),
		LossPts:    strings.TrimSpace(r.FormValue("loss_pts")),
	}
}

// parse validates the form against what the tally's preset allows. On
// failure it returns a localized message.
func (f periodForm) parse(r *http.Request, preset Preset) (Period, string) {
	p := Period{Name: f.Name}
	switch {
	case f.Name == "":
		return p, tr(r, "err.name_required")
	case tooLong(f.Name, maxPeriodNameLen):
		return p, tr(r, "err.name_long", maxPeriodNameLen)
	}
	var ok1, ok2 bool
	p.StartsOn, ok1 = parseDate(f.StartsOn)
	p.EndsOn, ok2 = parseDate(f.EndsOn)
	if !ok1 || !ok2 {
		return p, tr(r, "err.date")
	}
	if p.EndsOn.Before(p.StartsOn) {
		return p, tr(r, "err.date_order")
	}

	num := func(s string) (int, bool) {
		if s == "" {
			return 0, true
		}
		n, err := strconv.Atoi(s)
		return n, err == nil
	}
	bonus, okB := num(f.WinBonus)
	target, okT := num(f.Target)
	gameTarget, okG := num(f.GameTarget)
	winPts, okW := num(f.WinPts)
	drawPts, okD := num(f.DrawPts)
	lossPts, okL := num(f.LossPts)
	if !okB || !okT || !okG || !okW || !okD || !okL {
		return p, tr(r, "err.number")
	}
	if !preset.AllowsFormat(f.Format) {
		return p, tr(r, "score.err.format")
	}
	switch f.Format {
	case FormatOutcome:
		p.Scoring = ScoringConfig{Format: FormatOutcome, WinPts: winPts, DrawPts: drawPts, LossPts: lossPts}
	case FormatCounter:
		p.Scoring = DefaultCounter
	case FormatContinuous:
		p.Scoring = ScoringConfig{Format: FormatContinuous, Target: target, WinBonus: bonus}
	case FormatReset:
		p.Scoring = ScoringConfig{Format: FormatReset, Games: DefaultReset.Games, GameTarget: gameTarget, WinBonus: bonus}
	default:
		p.Scoring = ScoringConfig{Format: f.Format}
	}
	if err := p.Scoring.Validate(); err != nil {
		return p, trErr(r, err)
	}
	return p, ""
}

type periodsVM struct {
	tallyCtx
	Periods   []Period
	CurrentID string
	Form      periodForm
	Editing   *Period // nil on the list page
	HasScored bool    // Editing has scored matches: format locked, rule changes rescore
}

func (a *App) renderPeriods(w http.ResponseWriter, r *http.Request, vm periodsVM, status int) {
	ctx := r.Context()
	periods, err := a.store.ListPeriods(ctx, vm.Tally.ID)
	if err != nil {
		internalError(w, "list periods", err)
		return
	}
	vm.Periods = periods
	if cur, err := a.store.CurrentPeriod(ctx, vm.Tally.ID); err == nil {
		vm.CurrentID = cur.ID
	} else if !errors.Is(err, pgx.ErrNoRows) {
		internalError(w, "current period", err)
		return
	}
	if vm.Form == (periodForm{}) {
		vm.Form = a.newPeriodDefaults(r, vm.Tally, periods)
	}
	page := "periods"
	if vm.Editing != nil {
		page = "period_edit"
		if vm.HasScored, err = a.store.PeriodHasMatches(ctx, vm.Editing.ID); err != nil {
			internalError(w, "period has matches", err)
			return
		}
	}
	a.renderStatus(w, r, page, vm, status)
}

// newPeriodDefaults suggests the next season: numbered after the existing
// ones, starting the day after the latest one ends (or today), three months
// long, with the latest season's rule (or the preset's).
func (a *App) newPeriodDefaults(r *http.Request, t *Tally, existing []Period) periodForm {
	p := Period{
		Name:     tr(r, "period.default_name", len(existing)+1),
		StartsOn: today(),
		Scoring:  t.Preset().Scoring,
	}
	if len(existing) > 0 {
		latest := existing[0]
		p.Scoring = latest.Scoring
		if next := latest.EndsOn.AddDate(0, 0, 1); next.After(p.StartsOn) {
			p.StartsOn = next
		}
	}
	p.EndsOn = p.StartsOn.AddDate(0, 3, -1)
	return periodFormFrom(p)
}

func (a *App) handlePeriods(w http.ResponseWriter, r *http.Request) {
	tc, _, ok := a.adminTally(w, r, "periods")
	if !ok {
		return
	}
	a.renderPeriods(w, r, periodsVM{tallyCtx: tc}, http.StatusOK)
}

func (a *App) handleCreatePeriod(w http.ResponseWriter, r *http.Request) {
	tc, u, ok := a.adminTally(w, r, "periods")
	if !ok {
		return
	}
	limitBody(w, r, maxFormBytes)
	f := readPeriodForm(r)
	p, msg := f.parse(r, tc.Tally.Preset())
	if msg != "" {
		tc.Err = msg
		a.renderPeriods(w, r, periodsVM{tallyCtx: tc, Form: f}, http.StatusUnprocessableEntity)
		return
	}
	p.TallyID = tc.Tally.ID
	if _, err := a.store.CreatePeriod(r.Context(), u.ID, p); err != nil {
		internalError(w, "create period", err)
		return
	}
	redirectOK(w, r, tallyPath(tc.Tally, "/periods"), "period_created")
}

func (a *App) editablePeriod(w http.ResponseWriter, r *http.Request) (tallyCtx, *User, *Period, bool) {
	tc, u, ok := a.adminTally(w, r, "periods")
	if !ok {
		return tc, nil, nil, false
	}
	p, err := a.store.PeriodByID(r.Context(), tc.Tally.ID, r.PathValue("id"))
	if isNotFound(err) {
		a.notFound(w, r)
		return tc, nil, nil, false
	}
	if err != nil {
		internalError(w, "load period", err)
		return tc, nil, nil, false
	}
	return tc, u, p, true
}

func (a *App) handleEditPeriodForm(w http.ResponseWriter, r *http.Request) {
	tc, _, p, ok := a.editablePeriod(w, r)
	if !ok {
		return
	}
	a.renderPeriods(w, r, periodsVM{tallyCtx: tc, Editing: p, Form: periodFormFrom(*p)}, http.StatusOK)
}

func (a *App) handleEditPeriod(w http.ResponseWriter, r *http.Request) {
	tc, u, before, ok := a.editablePeriod(w, r)
	if !ok {
		return
	}
	limitBody(w, r, maxFormBytes)
	f := readPeriodForm(r)
	p, msg := f.parse(r, tc.Tally.Preset())
	if msg != "" {
		tc.Err = msg
		a.renderPeriods(w, r, periodsVM{tallyCtx: tc, Editing: before, Form: f}, http.StatusUnprocessableEntity)
		return
	}
	err := a.store.UpdatePeriod(r.Context(), u.ID, before, p)
	if errors.Is(err, ErrScoringLocked) || errors.Is(err, ErrRoundsOutside) {
		key := "err.scoring_locked"
		if errors.Is(err, ErrRoundsOutside) {
			key = "err.rounds_outside"
		}
		tc.Err = tr(r, key)
		a.renderPeriods(w, r, periodsVM{tallyCtx: tc, Editing: before, Form: f}, http.StatusUnprocessableEntity)
		return
	}
	if err != nil {
		internalError(w, "update period", err)
		return
	}
	redirectOK(w, r, tallyPath(tc.Tally, "/periods"), "period_saved")
}

// --- Settings & admins ---

type settingsVM struct {
	tallyCtx
	Admins      []Admin
	ShareURL    string
	FName       string
	FDesc       string
	FVisibility string
	FUsername   string
	typed       bool // F* hold a failed settings submit; don't reset them
	DefTitle1   string
	DefTitle2   string
}

func (a *App) renderSettings(w http.ResponseWriter, r *http.Request, vm settingsVM, status int) {
	admins, err := a.store.ListAdmins(r.Context(), vm.Tally.ID)
	if err != nil {
		internalError(w, "list admins", err)
		return
	}
	vm.Admins = admins
	vm.DefTitle1, vm.DefTitle2 = splitTitle(vm.Tally.Name)
	vm.ShareURL = baseURL(r) + tallyPath(vm.Tally, "")
	if !vm.typed {
		vm.FName, vm.FDesc, vm.FVisibility = vm.Tally.Name, vm.Tally.Description, vm.Tally.Visibility
	}
	a.renderStatus(w, r, "settings", vm, status)
}

func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	tc, _, ok := a.adminTally(w, r, "settings")
	if !ok {
		return
	}
	a.renderSettings(w, r, settingsVM{tallyCtx: tc}, http.StatusOK)
}

func (a *App) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	tc, u, ok := a.adminTally(w, r, "settings")
	if !ok {
		return
	}
	limitBody(w, r, maxFormBytes)
	vm := settingsVM{
		tallyCtx:    tc,
		FName:       strings.TrimSpace(r.FormValue("name")),
		FDesc:       strings.TrimSpace(r.FormValue("description")),
		FVisibility: r.FormValue("visibility"),
		typed:       true,
	}
	fail := func(key string, args ...any) {
		vm.Err = tr(r, key, args...)
		a.renderSettings(w, r, vm, http.StatusUnprocessableEntity)
	}
	switch {
	case vm.FName == "":
		fail("err.name_required")
		return
	case tooLong(vm.FName, maxTallyNameLen):
		fail("err.name_long", maxTallyNameLen)
		return
	case tooLong(vm.FDesc, maxTallyDescLen):
		fail("err.desc_long", maxTallyDescLen)
		return
	case vm.FVisibility != VisibilityLink && vm.FVisibility != VisibilityPrivate:
		fail("err.visibility")
		return
	}
	if err := a.store.UpdateTally(r.Context(), u.ID, tc.Tally, vm.FName, vm.FDesc, vm.FVisibility); err != nil {
		internalError(w, "update tally", err)
		return
	}
	redirectOK(w, r, tallyPath(tc.Tally, "/settings"), "settings_saved")
}

// ownerTally is adminTally restricted to the owner (managing admins).
func (a *App) ownerTally(w http.ResponseWriter, r *http.Request) (tallyCtx, *User, bool) {
	tc, u, ok := a.adminTally(w, r, "settings")
	if ok && !tc.IsOwner() {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return tc, nil, false
	}
	return tc, u, ok
}

func (a *App) handleAddAdmin(w http.ResponseWriter, r *http.Request) {
	tc, u, ok := a.ownerTally(w, r)
	if !ok {
		return
	}
	limitBody(w, r, maxFormBytes)
	username := normalizeUsername(r.FormValue("username"))
	err := a.store.AddAdmin(r.Context(), tc.Tally.ID, u.ID, username)
	var key string
	switch {
	case errors.Is(err, ErrUserNotFound):
		key = "err.admin_not_found"
	case errors.Is(err, ErrAlreadyAdmin):
		key = "err.admin_exists"
	case err != nil:
		internalError(w, "add admin", err)
		return
	default:
		redirectOK(w, r, tallyPath(tc.Tally, "/settings"), "admin_added")
		return
	}
	tc.Err = tr(r, key, username)
	a.renderSettings(w, r, settingsVM{tallyCtx: tc, FUsername: username}, http.StatusUnprocessableEntity)
}

func (a *App) handleRemoveAdmin(w http.ResponseWriter, r *http.Request) {
	tc, u, ok := a.ownerTally(w, r)
	if !ok {
		return
	}
	err := a.store.RemoveAdmin(r.Context(), tc.Tally.ID, u.ID, r.PathValue("uid"))
	if isNotFound(err) {
		a.notFound(w, r)
		return
	}
	if err != nil {
		internalError(w, "remove admin", err)
		return
	}
	redirectOK(w, r, tallyPath(tc.Tally, "/settings"), "admin_removed")
}
