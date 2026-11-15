package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

// The guide keeps a tally's menu only for that tally's admins; anyone else,
// and an unknown slug, get the plain guide.
func TestGuideKeepsTallyMenu(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	app := &App{store: s, tmpl: buildTemplates()}
	owner, _ := s.CreateUser(ctx, "owner", "x")
	other, _ := s.CreateUser(ctx, "other", "x")
	if _, err := s.CreateTally(ctx, owner.ID, Tally{Slug: "club", Name: "Club", Visibility: VisibilityPrivate}, Leaderboard{Template: "badminton_doubles"}); err != nil {
		t.Fatal(err)
	}
	guide := func(u *User, query string) string {
		r := httptest.NewRequest("GET", "/panduan"+query, nil)
		if u != nil {
			r = r.WithContext(context.WithValue(r.Context(), userCtxKey, u))
		}
		w := httptest.NewRecorder()
		app.handleGuide(w, r)
		if w.Code != 200 {
			t.Fatalf("GET /panduan%s: %d", query, w.Code)
		}
		return w.Body.String()
	}
	for _, c := range []struct {
		name  string
		u     *User
		query string
		menu  bool
	}{
		{"admin from the menu", owner, "?t=club", true},
		{"admin, plain guide", owner, "", false},
		{"not an admin", other, "?t=club", false},
		{"visitor", nil, "?t=club", false},
		{"unknown tally", owner, "?t=nope", false},
		{"unknown board", owner, "?t=club&board=nope", false},
	} {
		out := guide(c.u, c.query)
		if got := strings.Contains(out, `class="menubar"`); got != c.menu {
			t.Errorf("%s: menu %v, want %v", c.name, got, c.menu)
		}
		if !c.menu && strings.Contains(out, "/t/club") {
			t.Errorf("%s: the guide links to the tally", c.name)
		}
	}
}
