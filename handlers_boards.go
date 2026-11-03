package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// Leaderboards (papan, RFC-003): Setelan → Papan lists them; any admin can
// add, rename, reorder and delete (only while it has no seasons) one.

const maxBoardNameLen = 40

func boardsPath(t *Tally) string { return tallyPath(t, "/settings#papan") }

func (a *App) handleCreateBoard(w http.ResponseWriter, r *http.Request) {
	tc, u, ok := a.adminTally(w, r, "settings")
	if !ok {
		return
	}
	limitBody(w, r, maxFormBytes)
	done, ok := a.claimSubmit(w, r, withQuery(boardsPath(tc.Tally), "ok=board_added"))
	if !ok {
		return
	}
	saved := false
	defer done(&saved)
	vm := settingsVM{tallyCtx: tc, FBoardName: strings.TrimSpace(r.FormValue("name")),
		FBoardTemplate: r.FormValue("template"), FBoardSide: r.FormValue("side_size"), OpenBoard: true}
	fail := func(key string, args ...any) {
		vm.Err = tr(r, key, args...)
		a.renderSettings(w, r, vm, http.StatusUnprocessableEntity)
	}
	if tooLong(vm.FBoardName, maxBoardNameLen) {
		fail("err.board_name_long", maxBoardNameLen)
		return
	}
	preset, ok := presetByID(vm.FBoardTemplate)
	if !ok {
		fail("err.template")
		return
	}
	b := Leaderboard{Name: vm.FBoardName, Template: preset.ID}
	if len(preset.TeamSizes) > 0 {
		n, err := strconv.Atoi(vm.FBoardSide)
		if err != nil || !preset.AllowsTeamSize(n) {
			fail("err.team_size")
			return
		}
		b.SideSizeSet = &n
	}
	_, err := a.store.CreateLeaderboard(r.Context(), tc.Tally.ID, u.ID, b)
	if errors.Is(err, ErrTooManyBoards) {
		fail("err.boards_max", maxBoards)
		return
	}
	if err != nil {
		internalError(w, "create leaderboard", err)
		return
	}
	saved = true
	recordLeaderboardCreated(r.Context(), preset.ID)
	redirectOK(w, r, boardsPath(tc.Tally), "board_added")
}

// handleBoardAction renames (name), moves (up, down) or deletes a leaderboard.
func (a *App) handleBoardAction(w http.ResponseWriter, r *http.Request) {
	tc, u, ok := a.adminTally(w, r, "settings")
	if !ok {
		return
	}
	limitBody(w, r, maxFormBytes)
	ctx, id := r.Context(), r.PathValue("id")
	fail := func(key string, args ...any) {
		tc.Err = tr(r, key, args...)
		a.renderSettings(w, r, settingsVM{tallyCtx: tc}, http.StatusUnprocessableEntity)
	}
	var err error
	var key string
	switch r.FormValue("action") {
	case "rename":
		name := strings.TrimSpace(r.FormValue("name"))
		if tooLong(name, maxBoardNameLen) {
			fail("err.board_name_long", maxBoardNameLen)
			return
		}
		err, key = a.store.RenameLeaderboard(ctx, tc.Tally.ID, u.ID, id, name), "board_saved"
	case "up", "down":
		err, key = a.store.MoveLeaderboard(ctx, tc.Tally.ID, u.ID, id, r.FormValue("action") == "up"), "board_moved"
	case "delete":
		err, key = a.store.DeleteLeaderboard(ctx, tc.Tally.ID, u.ID, id), "board_deleted"
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	switch {
	case isNotFound(err):
		a.notFound(w, r)
	case errors.Is(err, ErrInUse):
		fail("err.board_in_use")
	case errors.Is(err, ErrLastBoard):
		fail("err.board_last")
	case err != nil:
		internalError(w, "leaderboard action", err)
	default:
		redirectOK(w, r, boardsPath(tc.Tally), key)
	}
}
