package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Sheet import (RFC-001 §7.4): upload an .xlsx on a season's page, check the
// preview, confirm. The preview carries the parsed sheet in a hidden field, so
// nothing is kept on the server between the two steps; confirm checks it all
// again.

const (
	maxSheetBytes   = 2 << 20 // upload limit
	maxPayloadBytes = 1 << 20 // the confirm form with the parsed sheet
)

// importPayload is what the preview hands to confirm.
type importPayload struct {
	File  string
	Sheet *sheetParse
}

type importDayVM struct {
	Round    Round // date and week label as it will be after the import
	Players  int
	Plays    int
	Points   int
	Status   string // "new", "fill" (exists, no points yet), "skip", "replace"
	Existing int    // points already on that play day
}

type importVM struct {
	tallyCtx
	Period     *Period
	Preview    bool
	File       string
	Sheet      *sheetParse
	Days       []importDayVM
	NewPlayers []string
	Players    int
	Plays      int
	Points     int
	Target     int
	Errors     []string
	Outside    bool // some dates fall outside the season
	From, To   time.Time
	Extend     bool
	Replace    bool
	Payload    string
	Writes     int // play days the import will write
}

// scoreLimit is the most points one play can earn when points are entered per
// player, and false when the season's format doesn't support that: only a
// race to a target (badminton to 42) does for now.
func scoreLimit(t *Tally, p *Period) (int, bool) {
	c := p.Scoring
	if t.Preset().IsCounter() || c.Format != FormatContinuous {
		return 0, false
	}
	return c.Target + c.WinBonus, true
}

func (a *App) importPeriod(w http.ResponseWriter, r *http.Request) (tallyCtx, *User, *Period, int, bool) {
	tc, u, p, ok := a.editablePeriod(w, r)
	if !ok {
		return tc, nil, nil, 0, false
	}
	limit, ok := scoreLimit(tc.Tally, p)
	if !ok {
		a.notFound(w, r)
		return tc, nil, nil, 0, false
	}
	return tc, u, p, limit, true
}

func (a *App) handleImportForm(w http.ResponseWriter, r *http.Request) {
	tc, _, p, limit, ok := a.importPeriod(w, r)
	if !ok {
		return
	}
	a.renderStatus(w, r, "period_import", importVM{tallyCtx: tc, Period: p, Target: limit}, http.StatusOK)
}

// handleImportPreview reads the uploaded sheet and shows what would happen.
func (a *App) handleImportPreview(w http.ResponseWriter, r *http.Request) {
	tc, _, p, limit, ok := a.importPeriod(w, r)
	if !ok {
		return
	}
	vm := importVM{tallyCtx: tc, Period: p, Target: limit}
	fail := func(key string, args ...any) {
		vm.Err = tr(r, key, args...)
		a.renderStatus(w, r, "period_import", vm, http.StatusUnprocessableEntity)
	}
	limitBody(w, r, maxSheetBytes+maxFormBytes)
	if err := r.ParseMultipartForm(maxSheetBytes + maxFormBytes); err != nil {
		fail("import.err.too_big", maxSheetBytes>>20)
		return
	}
	f, hdr, err := r.FormFile("sheet")
	if err != nil {
		fail("import.err.no_file")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSheetBytes+1))
	if err != nil || len(data) > maxSheetBytes {
		fail("import.err.too_big", maxSheetBytes>>20)
		return
	}
	g, err := readFirstSheet(data)
	if err != nil {
		fail("import.err.not_xlsx")
		return
	}
	vm.File = filepath.Base(hdr.Filename)
	vm.Sheet = parsePointsSheet(g, limit, p.StartsOn.Year())
	vm.Replace = r.FormValue("replace") == "1"
	if !a.planImport(w, r, &vm) {
		return
	}
	a.renderStatus(w, r, "period_import", vm, http.StatusOK)
}

// planImport checks the parsed sheet against the season and the tally, and
// fills the preview. It returns false after writing an error response.
func (a *App) planImport(w http.ResponseWriter, r *http.Request, vm *importVM) bool {
	ctx := r.Context()
	vm.Preview = true
	for _, e := range vm.Sheet.Errors {
		vm.Errors = append(vm.Errors, tr(r, "import.err."+e.Key, e.Args...))
	}
	if len(vm.Sheet.Days) == 0 && len(vm.Errors) == 0 {
		vm.Errors = append(vm.Errors, tr(r, "import.err.no_scores"))
	}

	rounds, err := a.store.RoundsForPeriod(ctx, vm.Period.ID)
	if err != nil {
		internalError(w, "list rounds", err)
		return false
	}
	players, err := a.store.ListParticipants(ctx, vm.Tally.ID)
	if err != nil {
		internalError(w, "list players", err)
		return false
	}
	known := map[string]bool{}
	for _, pl := range players {
		known[strings.ToLower(pl.Name)] = true
	}
	existing := map[time.Time]Round{}
	dates := []time.Time{}
	for _, rd := range rounds {
		existing[rd.PlayedOn] = rd
		dates = append(dates, rd.PlayedOn)
	}

	vm.From, vm.To = vm.Period.StartsOn, vm.Period.EndsOn
	seen, newSeen := map[string]bool{}, map[string]bool{}
	for _, d := range vm.Sheet.Days {
		if d.Date.Before(vm.From) {
			vm.From = d.Date
		}
		if d.Date.After(vm.To) {
			vm.To = d.Date
		}
		if _, ok := existing[d.Date]; !ok {
			dates = append(dates, d.Date)
		}
		day := importDayVM{Round: Round{PlayedOn: d.Date}, Players: len(d.Players), Status: "new"}
		if rd, ok := existing[d.Date]; ok {
			day.Existing = rd.Awards
			switch {
			case rd.Awards == 0:
				day.Status = "fill"
			case vm.Replace:
				day.Status = "replace"
			default:
				day.Status = "skip"
			}
		}
		for _, pl := range d.Players {
			day.Plays += len(pl.Points)
			for _, n := range pl.Points {
				day.Points += n
			}
			key := strings.ToLower(pl.Name)
			if !known[key] && !newSeen[key] {
				newSeen[key] = true
				vm.NewPlayers = append(vm.NewPlayers, pl.Name)
			}
			seen[key] = true
		}
		if day.Status != "skip" {
			vm.Writes++
			vm.Plays += day.Plays
			vm.Points += day.Points
		}
		vm.Days = append(vm.Days, day)
	}
	vm.Players = len(seen)
	vm.Outside = vm.From.Before(vm.Period.StartsOn) || vm.To.After(vm.Period.EndsOn)

	// Week labels as they will read once the sheet's play days are in.
	slices.SortFunc(dates, func(a, b time.Time) int { return a.Compare(b) })
	for i := range vm.Days {
		d := &vm.Days[i].Round
		for _, other := range dates {
			if other.Year() == d.PlayedOn.Year() && other.Month() == d.PlayedOn.Month() && (Round{PlayedOn: other}).Week() == d.Week() {
				d.WeekDays++
				if !other.After(d.PlayedOn) {
					d.WeekPart++
				}
			}
		}
	}
	slices.SortStableFunc(vm.NewPlayers, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })

	b, err := json.Marshal(importPayload{File: vm.File, Sheet: vm.Sheet})
	if err != nil {
		internalError(w, "import payload", err)
		return false
	}
	vm.Payload = string(b)
	return true
}

// checkPayload re-validates a sheet coming back from the preview form.
func checkPayload(s *sheetParse, limit int) bool {
	if s == nil || len(s.Days) == 0 || len(s.Errors) > 0 {
		return false
	}
	dates := map[time.Time]bool{}
	for _, d := range s.Days {
		if d.Date.IsZero() || dates[d.Date] || len(d.Players) == 0 {
			return false
		}
		dates[d.Date] = true
		names := map[string]bool{}
		for _, pl := range d.Players {
			name := normName(pl.Name)
			key := strings.ToLower(name)
			if name != pl.Name || name == "" || tooLong(name, maxPlayerNameLen) || names[key] ||
				len(pl.Points) == 0 || len(pl.Points) > maxPlaysPerDay {
				return false
			}
			names[key] = true
			for _, n := range pl.Points {
				if n < 0 || n > limit {
					return false
				}
			}
		}
	}
	return true
}

// handleImportConfirm writes the previewed sheet.
func (a *App) handleImportConfirm(w http.ResponseWriter, r *http.Request) {
	tc, u, p, limit, ok := a.importPeriod(w, r)
	if !ok {
		return
	}
	limitBody(w, r, maxPayloadBytes)
	back := tallyPath(tc.Tally, "/periods/"+p.ID)
	done, ok := a.claimSubmit(w, r, back+"?ok=import_done")
	if !ok {
		return
	}
	saved := false
	defer done(&saved)

	var pl importPayload
	if err := json.Unmarshal([]byte(r.FormValue("payload")), &pl); err != nil || !checkPayload(pl.Sheet, limit) {
		http.Error(w, "bad import data; upload the sheet again", http.StatusBadRequest)
		return
	}
	vm := importVM{tallyCtx: tc, Period: p, Target: limit, File: filepath.Base(pl.File), Sheet: pl.Sheet,
		Replace: r.FormValue("replace") == "1", Extend: r.FormValue("extend") == "1"}
	if !a.planImport(w, r, &vm) {
		return
	}
	if vm.Outside && !vm.Extend {
		vm.Err = tr(r, "import.err.outside_unticked")
		a.renderStatus(w, r, "period_import", vm, http.StatusUnprocessableEntity)
		return
	}
	days := make([]ImportDay, 0, len(pl.Sheet.Days))
	for _, d := range pl.Sheet.Days {
		days = append(days, ImportDay{Date: d.Date, Plays: d.Players})
	}
	res, err := a.store.ImportScores(r.Context(), tc.Tally.ID, u.ID, p, days, vm.Replace, vm.From, vm.To, vm.File)
	if errors.Is(err, ErrNameTaken) {
		vm.Err = tr(r, "err.score_retry")
		a.renderStatus(w, r, "period_import", vm, http.StatusConflict)
		return
	}
	if err != nil {
		internalError(w, "import scores", err)
		return
	}
	saved = true
	recordScores(r.Context(), res.Entries)
	recordPlayersAdded(r.Context(), res.NewPlayers)
	redirectOK(w, r, back, "import_done")
}
