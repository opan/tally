package main

import (
	"errors"
	"net/http"
	"strconv"
)

// Level (RFC-006): Setelan → Level lists the tally's levels, strongest first.
// Any admin can set up the default list, add, rename, reorder and remove one;
// a rename or removal updates the players who have it.

func levelsPath(t *Tally) string { return tallyPath(t, "/settings#level") }

func (a *App) handleLevelAction(w http.ResponseWriter, r *http.Request) {
	tc, u, ok := a.adminTally(w, r, "settings")
	if !ok {
		return
	}
	limitBody(w, r, maxFormBytes)
	ctx, level := r.Context(), r.FormValue("level")
	name := cleanLevel(r.FormValue("name"))
	vm := settingsVM{tallyCtx: tc}
	fail := func(key string, args ...any) {
		vm.Err = tr(r, key, args...)
		a.renderSettings(w, r, vm, http.StatusUnprocessableEntity)
	}
	checkName := func() bool {
		switch {
		case name == "":
			fail("err.level_required")
		case tooLong(name, maxLevelLen):
			fail("err.level_long", maxLevelLen)
		default:
			return true
		}
		return false
	}

	var err error
	var key string
	n := 0
	switch r.FormValue("action") {
	case "default":
		err, key = a.store.UseDefaultLevels(ctx, tc.Tally.ID, u.ID), "levels_added"
	case "add":
		vm.FLevel = name
		if !checkName() {
			return
		}
		err, key = a.store.AddLevel(ctx, tc.Tally.ID, u.ID, name), "level_added"
	case "rename":
		if !checkName() {
			return
		}
		n, err = a.store.RenameLevel(ctx, tc.Tally.ID, u.ID, level, name)
		key = "level_renamed"
	case "up", "down":
		err, key = a.store.MoveLevel(ctx, tc.Tally.ID, u.ID, level, r.FormValue("action") == "up"), "level_moved"
	case "delete":
		n, err = a.store.DeleteLevel(ctx, tc.Tally.ID, u.ID, level)
		key = "level_deleted"
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	switch {
	case isNotFound(err):
		fail("err.level_gone")
	case errors.Is(err, ErrLevelExists):
		fail("err.level_exists", name)
	case errors.Is(err, ErrTooManyLevels):
		fail("err.levels_max", maxLevels)
	case err != nil:
		internalError(w, "level action", err)
	default:
		path := levelsPath(tc.Tally)
		if n > 0 {
			path = withQuery(path, "n="+strconv.Itoa(n))
		}
		redirectOK(w, r, path, key)
	}
}
