package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// An archived season is read-only, out of the pickers and never current;
// any admin of the tally can archive and unarchive it.
func TestArchiveSeason(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	app := &App{store: s, tmpl: buildTemplates()}
	owner, _ := s.CreateUser(ctx, "owner", "x")
	admin, _ := s.CreateUser(ctx, "admin", "x")
	tl, err := s.CreateTally(ctx, owner.ID, Tally{Slug: "club", Name: "Club", Visibility: VisibilityLink}, Leaderboard{Template: "badminton_doubles"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddAdmin(ctx, tl.ID, owner.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	old := today().AddDate(0, -4, 0)
	s1, _ := s.CreatePeriod(ctx, owner.ID, Period{TallyID: tl.ID, LeaderboardID: defaultBoard(t, s, tl.ID), Name: "S1", StartsOn: old, EndsOn: old.AddDate(0, 2, 0), Scoring: DefaultContinuous})
	s2, _ := s.CreatePeriod(ctx, owner.ID, Period{TallyID: tl.ID, LeaderboardID: defaultBoard(t, s, tl.ID), Name: "S2", StartsOn: today().AddDate(0, -1, 0), EndsOn: today().AddDate(0, 2, 0), Scoring: DefaultContinuous})
	rid, _ := s.CreateRound(ctx, tl.ID, owner.ID, s1.ID, old, 1, nil)
	rd, _ := s.RoundByID(ctx, tl.ID, rid)
	if err := s.AddScore(ctx, tl.ID, owner.ID, rd, "", "Jacque Oui", 42, 7); err != nil {
		t.Fatal(err)
	}
	scores, _ := s.ScoreAwards(ctx, rid)

	do := func(method, path string, h http.HandlerFunc, vals map[string]string, form url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		if form != nil {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		r.SetPathValue("slug", "club")
		for k, v := range vals {
			r.SetPathValue(k, v)
		}
		r = r.WithContext(context.WithValue(r.Context(), userCtxKey, admin))
		w := httptest.NewRecorder()
		h(w, r)
		return w
	}
	archive := func(id, v string) *httptest.ResponseRecorder {
		return do("POST", "/t/club/periods/"+id+"/archive", app.handleArchivePeriod, map[string]string{"id": id}, url.Values{"archived": {v}})
	}

	// S2 is current; archiving it falls back to S1, archiving both leaves none.
	if w := archive(s2.ID, "1"); w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "ok=season_archived") {
		t.Fatalf("archive by an admin: %d %s", w.Code, w.Header().Get("Location"))
	}
	if cur, _ := s.CurrentPeriod(ctx, tl.ID, defaultBoard(t, s, tl.ID)); cur == nil || cur.ID != s1.ID {
		t.Errorf("current after archiving S2: %v, want S1", cur)
	}
	if w := archive(s2.ID, "1"); w.Code != http.StatusSeeOther {
		t.Errorf("archiving twice: %d, want a redirect", w.Code)
	}
	archive(s1.ID, "1")
	if _, err := s.CurrentPeriod(ctx, tl.ID, defaultBoard(t, s, tl.ID)); !isNotFound(err) {
		t.Errorf("current with every season archived: %v, want none", err)
	}
	if p, _ := s.PeriodByID(ctx, tl.ID, s1.ID); !p.Archived() || p.Status() != SeasonArchived {
		t.Errorf("S1 after archiving: archived=%v status=%s", p.Archived(), p.Status())
	}

	// Writes to the archived season bounce to its read-only pages.
	seasonPage, dayPage := "/t/club/periods/"+s1.ID, "/t/club/rounds/"+rid
	for _, c := range []struct {
		name string
		w    *httptest.ResponseRecorder
		want string
	}{
		{"edit form", do("GET", "/", app.handleEditPeriodForm, map[string]string{"id": s1.ID}, nil), seasonPage},
		{"edit", do("POST", "/", app.handleEditPeriod, map[string]string{"id": s1.ID}, url.Values{"name": {"X"}}), seasonPage},
		{"import", do("GET", "/", app.handleImportForm, map[string]string{"id": s1.ID}, nil), seasonPage},
		{"score", do("POST", "/", app.handleAddScore, map[string]string{"rid": rid}, url.Values{"player": {"Jacque Oui"}, "points": {"40"}}), dayPage},
		{"setup", do("POST", "/", app.handleRoundSetup, map[string]string{"rid": rid}, url.Values{}), dayPage},
		{"delete score", do("POST", "/", app.handleDeleteAward, map[string]string{"aid": scores[0].ID}, url.Values{}), dayPage},
	} {
		if c.w.Code != http.StatusSeeOther || c.w.Header().Get("Location") != c.want {
			t.Errorf("%s: %d to %q, want a redirect to %q", c.name, c.w.Code, c.w.Header().Get("Location"), c.want)
		}
	}
	if w := do("POST", "/", app.handleCreateRound, nil, url.Values{"played_on": {old.Format(dateLayout)}}); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("new play day in an archived season: %d, want 422", w.Code)
	}
	if n, _ := s.ScoreAwards(ctx, rid); len(n) != 1 {
		t.Errorf("scores after refused writes: %d, want 1", len(n))
	}

	// The play day is still readable, without forms; the season page offers unarchiving.
	w := do("GET", dayPage, app.handleRound, map[string]string{"rid": rid}, nil)
	if body := w.Body.String(); w.Code != 200 || !strings.Contains(body, "Jacque Oui") || strings.Contains(body, `action="/t/club/rounds`) {
		t.Errorf("archived play day: %d, want a read-only page with the scores", w.Code)
	}
	w = do("GET", seasonPage, app.handlePeriodDetail, map[string]string{"id": s1.ID}, nil)
	if body := w.Body.String(); !strings.Contains(body, `name="archived" value="0"`) || strings.Contains(body, "/edit") {
		t.Errorf("archived season page should offer unarchive and no edit link")
	}
	// The board picker keeps an archived season only while it's on screen.
	w = do("GET", "/t/club?season="+s1.ID, app.handleTally, nil, nil)
	if body := w.Body.String(); w.Code != 200 || !strings.Contains(body, "Jacque Oui") {
		t.Errorf("board of an archived season: %d", w.Code)
	}
	all, _ := s.ListPeriods(ctx, tl.ID, defaultBoard(t, s, tl.ID))
	if got := pickerPeriods(all, nil); len(got) != 0 {
		t.Errorf("picker with both archived: %d seasons, want 0", len(got))
	}

	if w := archive(s1.ID, "0"); !strings.Contains(w.Header().Get("Location"), "ok=season_unarchived") {
		t.Fatalf("unarchive: %s", w.Header().Get("Location"))
	}
	if w := do("GET", "/", app.handleEditPeriodForm, map[string]string{"id": s1.ID}, nil); w.Code != 200 {
		t.Errorf("edit form after unarchiving: %d, want 200", w.Code)
	}
	var n int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tally_id = $1 AND action IN ('archive', 'unarchive')`, tl.ID).Scan(&n)
	if n != 3 {
		t.Errorf("audit entries: %d, want 3", n)
	}
}
