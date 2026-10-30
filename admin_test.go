package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Site admins can open any tally's read-only pages, private ones included;
// each view is logged with the tally, and everyone else gets a 404.
func TestAdminTallyPages(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const adminSub, userSub = "6f1c2a0e-8f4b-4f51-9a39-0d6f3d1e2b7a", "0b8d2f4e-1c3a-4e5f-8a7b-9c0d1e2f3a4b"
	aio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		admin := strings.Contains(r.URL.Path, adminSub)
		w.Write([]byte(`{"success":true,"data":{"admin":` + map[bool]string{true: "true", false: "false"}[admin] + `,"blocked":false}}`))
	}))
	defer aio.Close()
	app := &App{store: s, tmpl: buildTemplates(),
		aio: &AioAuth{enabled: true, clientID: "tally", clientSecret: "x", internalURL: aio.URL, httpClient: &http.Client{Timeout: time.Second}}}

	siteAdmin, err := s.CreateAioUser(ctx, adminSub, "boss")
	if err != nil {
		t.Fatal(err)
	}
	siteAdmin.AioUserID = adminSub
	owner, _ := s.CreateAioUser(ctx, userSub, "owner")
	owner.AioUserID = userSub
	tl, err := s.CreateTally(ctx, owner.ID, Tally{Slug: "rahasia", Name: "Rahasia", Template: "badminton_doubles", Visibility: VisibilityPrivate})
	if err != nil {
		t.Fatal(err)
	}
	d := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	period, _ := s.CreatePeriod(ctx, owner.ID, Period{TallyID: tl.ID, Name: "S1", StartsOn: d, EndsOn: d.AddDate(0, 2, 0), Scoring: DefaultContinuous})
	rid, _ := s.CreateRound(ctx, tl.ID, owner.ID, period.ID, d, 1, nil)
	rd, _ := s.RoundByID(ctx, tl.ID, rid)
	if err := s.AddScore(ctx, tl.ID, owner.ID, rd, "", "Ana", 42, 7); err != nil {
		t.Fatal(err)
	}
	players, _ := s.ListParticipants(ctx, tl.ID)

	get := func(u *User, h http.HandlerFunc, path string, vals map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		for k, v := range vals {
			r.SetPathValue(k, v)
		}
		r = r.WithContext(context.WithValue(r.Context(), userCtxKey, u))
		w := httptest.NewRecorder()
		h(w, r)
		return w
	}
	pages := []struct {
		name string
		h    http.HandlerFunc
		vals map[string]string
		want string
	}{
		{"tally", app.handleAdminTally, map[string]string{"id": tl.ID}, "/admin/tallies/" + tl.ID + "/seasons/" + period.ID},
		{"season", app.handleAdminSeason, map[string]string{"id": tl.ID, "pid": period.ID}, "/admin/tallies/" + tl.ID + "/days/" + rid},
		{"player", app.handleAdminPlayer, map[string]string{"id": tl.ID, "pid": players[0].ID}, "Ana"},
		{"day", app.handleAdminDay, map[string]string{"id": tl.ID, "rid": rid}, ">42<"},
	}
	for _, p := range pages {
		w := get(siteAdmin, p.h, "/admin/x", p.vals)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, p.want) {
			t.Errorf("%s: %d, want 200 with %q", p.name, w.Code, p.want)
		}
		for _, bad := range []string{`action="/t/`, "/periods/" + period.ID + "/edit", "/import"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s: read-only page contains %q", p.name, bad)
			}
		}
		if w := get(owner, p.h, "/admin/x", p.vals); w.Code != http.StatusNotFound {
			t.Errorf("%s for a non-admin: %d, want 404", p.name, w.Code)
		}
	}
	var logged int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM admin_views WHERE admin_id = $1 AND tally_id = $2 AND subject_user_id = $3`,
		siteAdmin.ID, tl.ID, owner.ID).Scan(&logged); err != nil {
		t.Fatal(err)
	}
	if logged != len(pages) {
		t.Errorf("logged %d admin views, want %d", logged, len(pages))
	}
	// A play day of another tally isn't reachable through this one.
	other, _ := s.CreateTally(ctx, owner.ID, Tally{Slug: "lain", Name: "Lain", Template: "badminton_doubles", Visibility: VisibilityLink})
	if w := get(siteAdmin, app.handleAdminDay, "/admin/x", map[string]string{"id": other.ID, "rid": rid}); w.Code != http.StatusNotFound {
		t.Errorf("play day through the wrong tally: %d, want 404", w.Code)
	}
}
