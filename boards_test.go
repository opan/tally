package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The RFC-003 upgrade gives every existing tally one leaderboard with the
// type it had and moves its seasons onto it; running it again changes nothing.
func TestLeaderboardUpgrade(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	// Back to the shape before RFC-003.
	if _, err := s.pool.Exec(ctx, `ALTER TABLE periods DROP COLUMN leaderboard_id; DROP TABLE leaderboards`); err != nil {
		t.Fatal(err)
	}
	u, _ := s.CreateUser(ctx, "owner", "x")
	seven := 7
	old := []struct {
		slug, template string
		side           *int
		seasons        int
	}{
		{"ganda", "badminton_doubles", nil, 2},
		{"tunggal", "badminton_singles", nil, 1},
		{"futsal", "team", &seven, 1},
		{"catur", "duel", nil, 0},
		{"sepeda", "counter", nil, 1},
	}
	d := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	ids := map[string]string{}
	for _, o := range old {
		var id string
		if err := s.pool.QueryRow(ctx,
			`INSERT INTO tallies (owner_id, slug, name, template, side_size) VALUES ($1, $2, $2, $3, $4) RETURNING id`,
			u.ID, o.slug, o.template, o.side).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[o.slug] = id
		for i := range o.seasons {
			var pid string
			if err := s.pool.QueryRow(ctx,
				`INSERT INTO periods (tally_id, name, starts_on, ends_on, scoring) VALUES ($1, $2, $3, $4, $5::jsonb) RETURNING id`,
				id, "S"+string(rune('1'+i)), d.AddDate(0, 3*i, 0), d.AddDate(0, 3*i+2, 0), scoringJSON(DefaultContinuous)).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			if _, err := s.pool.Exec(ctx, `INSERT INTO rounds (period_id, played_on) VALUES ($1, $2)`, pid, d.AddDate(0, 3*i, 0)); err != nil {
				t.Fatal(err)
			}
		}
	}

	for range 2 {
		if _, err := s.pool.Exec(ctx, schemaSQL); err != nil {
			t.Fatal(err)
		}
	}

	for _, o := range old {
		boards, err := s.ListLeaderboards(ctx, ids[o.slug])
		if err != nil {
			t.Fatal(err)
		}
		if len(boards) != 1 {
			t.Fatalf("%s: %d leaderboards, want 1", o.slug, len(boards))
		}
		b := boards[0]
		want, _ := presetByID(o.template)
		if b.Template != o.template || b.Slug != boardSlug(want) || b.Position != 0 || b.Name != "" {
			t.Errorf("%s: leaderboard %+v", o.slug, b)
		}
		if (o.side == nil) != (b.SideSizeSet == nil) || (o.side != nil && *b.SideSizeSet != *o.side) {
			t.Errorf("%s: side size %v, want %v", o.slug, b.SideSizeSet, o.side)
		}
		periods, _ := s.ListPeriods(ctx, ids[o.slug], b.ID)
		if len(periods) != o.seasons {
			t.Errorf("%s: %d seasons on the leaderboard, want %d", o.slug, len(periods), o.seasons)
		}
		if o.seasons > 0 {
			if _, err := s.RoundByDate(ctx, ids[o.slug], b.ID, d); err != nil {
				t.Errorf("%s: play day on %s: %v", o.slug, d.Format(dateLayout), err)
			}
		}
	}
	if b, _ := s.DefaultLeaderboard(ctx, ids["futsal"]); b.SideSize() != 7 || b.Preset().ID != "team" {
		t.Errorf("futsal: %d per side, preset %s", b.SideSize(), b.Preset().ID)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO periods (tally_id, name, starts_on, ends_on, scoring) VALUES ($1, 'x', $2, $2, '{}')`, ids["catur"], d); err == nil {
		t.Error("a season without a leaderboard was accepted")
	}
}

// A season's leaderboard must be one of its own tally's.
func TestSeasonLeaderboardInSameTally(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, _ := s.CreateUser(ctx, "owner", "x")
	a, _ := s.CreateTally(ctx, u.ID, Tally{Slug: "a", Name: "A", Visibility: VisibilityLink}, Leaderboard{Template: "badminton_doubles"})
	b, _ := s.CreateTally(ctx, u.ID, Tally{Slug: "b", Name: "B", Visibility: VisibilityLink}, Leaderboard{Template: "counter"})
	d := today()
	_, err := s.CreatePeriod(ctx, u.ID, Period{TallyID: a.ID, LeaderboardID: defaultBoard(t, s, b.ID), Name: "S1", StartsOn: d, EndsOn: d, Scoring: DefaultContinuous})
	if !isFKViolation(err) {
		t.Errorf("season on another tally's leaderboard: %v, want a foreign key violation", err)
	}
	if lb, _ := s.DefaultLeaderboard(ctx, b.ID); lb.Slug != "counter" || lb.Preset().ID != "counter" {
		t.Errorf("new tally's leaderboard: %+v", lb)
	}
}

// Two leaderboards in one tally keep their seasons and play days apart, and
// pages pick theirs from ?board= or the season they show. Phase 1 has no way
// to add a second one in the app, so it's inserted here.
func TestTwoLeaderboards(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	owner, _ := s.CreateUser(ctx, "owner", "x")
	tl, _ := s.CreateTally(ctx, owner.ID, Tally{Slug: "club", Name: "Club", Visibility: VisibilityLink}, Leaderboard{Template: "badminton_doubles"})
	bad := defaultBoard(t, s, tl.ID)
	var basket string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO leaderboards (tally_id, slug, name, template, side_size, position) VALUES ($1, 'basket', 'Basket', 'team', 5, 1) RETURNING id`,
		tl.ID).Scan(&basket); err != nil {
		t.Fatal(err)
	}
	d := today().AddDate(0, 0, -3)
	mk := func(board, name string, scoring ScoringConfig) *Period {
		p, err := s.CreatePeriod(ctx, owner.ID, Period{TallyID: tl.ID, LeaderboardID: board, Name: name, StartsOn: d, EndsOn: d.AddDate(0, 2, 0), Scoring: scoring})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	sb, sk := mk(bad, "Musim badminton", DefaultContinuous), mk(basket, "Musim basket", DefaultOutcome)
	rb, err1 := s.CreateRound(ctx, tl.ID, owner.ID, sb.ID, d, 1, nil)
	rk, err2 := s.CreateRound(ctx, tl.ID, owner.ID, sk.ID, d, 1, nil)
	if err1 != nil || err2 != nil {
		t.Fatalf("play days on the same date: %v, %v", err1, err2)
	}

	for _, c := range []struct{ board, season, round string }{{bad, sb.ID, rb}, {basket, sk.ID, rk}} {
		if p, _ := s.CurrentPeriod(ctx, tl.ID, c.board); p == nil || p.ID != c.season {
			t.Errorf("current season of %s: %v", c.board, p)
		}
		if p, _ := s.PeriodForDate(ctx, tl.ID, c.board, d); p == nil || p.ID != c.season {
			t.Errorf("season for the date on %s: %v", c.board, p)
		}
		if r, _ := s.RoundByDate(ctx, tl.ID, c.board, d); r == nil || r.ID != c.round {
			t.Errorf("play day for the date on %s: %v", c.board, r)
		}
		if ps, _ := s.ListPeriods(ctx, tl.ID, c.board); len(ps) != 1 || ps[0].ID != c.season {
			t.Errorf("seasons of %s: %v", c.board, ps)
		}
	}

	app := &App{store: s, tmpl: buildTemplates(), aio: &AioAuth{}}
	get := func(h http.HandlerFunc, path string, vals map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.SetPathValue("slug", "club")
		for k, v := range vals {
			r.SetPathValue(k, v)
		}
		r = r.WithContext(context.WithValue(r.Context(), userCtxKey, owner))
		w := httptest.NewRecorder()
		h(w, r)
		return w
	}
	pages := []struct {
		name, path string
		h          http.HandlerFunc
		vals       map[string]string
		want, not  string
	}{
		{"board, default", "/t/club", app.handleTally, nil, "Musim badminton", "Musim basket"},
		{"board, ?board=", "/t/club?board=basket", app.handleTally, nil, "Musim basket", "Musim badminton"},
		{"board, ?season=", "/t/club?season=" + sk.ID, app.handleTally, nil, "Musim basket", "Musim badminton"},
		{"seasons, ?board=", "/t/club/periods?board=basket", app.handlePeriods, nil, "Musim basket", "Musim badminton"},
		{"play, default", "/t/club/play", app.handlePlay, nil, "Musim badminton", "Musim basket"},
		{"season page", "/t/club/periods/" + sk.ID, app.handlePeriodDetail, map[string]string{"id": sk.ID}, "Musim basket", "Musim badminton"},
		{"play day", "/t/club/rounds/" + rk, app.handleRound, map[string]string{"rid": rk}, "Musim basket", "Musim badminton"},
	}
	for _, p := range pages {
		w := get(p.h, p.path, p.vals)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, p.want) || strings.Contains(body, p.not) {
			t.Errorf("%s: %d, want 200 with %q and without %q", p.name, w.Code, p.want, p.not)
		}
	}
	// The play day page follows its season's leaderboard: team of 5.
	if body := get(app.handleRound, "/t/club/rounds/"+rk+"?tab=match", map[string]string{"rid": rk}).Body.String(); !strings.Contains(body, translate("id", "tally.per_side", 5)) {
		t.Error("basket play day doesn't show the team preset's header")
	}
	if w := get(app.handleTally, "/t/club?board=nope", nil); w.Code != http.StatusNotFound {
		t.Errorf("unknown ?board=: %d, want 404", w.Code)
	}
}

// Every tally page renders for each kind of leaderboard: the templates read
// the type from the page's leaderboard, which the compiler can't check.
func TestTallyPagesRender(t *testing.T) {
	for _, c := range []struct {
		template string
		side     *int
		scoring  ScoringConfig
	}{
		{"badminton_doubles", nil, DefaultContinuous},
		{"team", func() *int { n := 6; return &n }(), DefaultOutcome},
		{"counter", nil, DefaultCounter},
	} {
		t.Run(c.template, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			owner, _ := s.CreateUser(ctx, "owner", "x")
			tl, err := s.CreateTally(ctx, owner.ID, Tally{Slug: "club", Name: "Badminton Sampai Pingsan", Visibility: VisibilityLink},
				Leaderboard{Template: c.template, SideSizeSet: c.side})
			if err != nil {
				t.Fatal(err)
			}
			d := today().AddDate(0, 0, -3)
			p, err := s.CreatePeriod(ctx, owner.ID, Period{TallyID: tl.ID, LeaderboardID: defaultBoard(t, s, tl.ID), Name: "Musim #1", StartsOn: d, EndsOn: d.AddDate(0, 2, 0), Scoring: c.scoring})
			if err != nil {
				t.Fatal(err)
			}
			rid, _ := s.CreateRound(ctx, tl.ID, owner.ID, p.ID, d, 1, nil)
			rd, _ := s.RoundByID(ctx, tl.ID, rid)
			if _, _, err := s.AddParticipants(ctx, tl.ID, owner.ID, []string{"Jacque Oui", "Prabski", "Bahlul"}); err != nil {
				t.Fatal(err)
			}
			players, _ := s.ListParticipants(ctx, tl.ID)
			if err := s.AddAwards(ctx, tl.ID, owner.ID, rd, []AwardInput{{ParticipantID: players[0].ID, Points: 10}}, "Bonus"); err != nil {
				t.Fatal(err)
			}
			// A linked player, for Mabar saya on the home page.
			if _, err := s.pool.Exec(ctx, `UPDATE participants SET user_id = $1 WHERE id = $2`, owner.ID, players[0].ID); err != nil {
				t.Fatal(err)
			}

			app := &App{store: s, tmpl: buildTemplates(), aio: &AioAuth{}}
			for _, pg := range []struct {
				path string
				h    http.HandlerFunc
				vals map[string]string
			}{
				{"/", app.handleHome, nil},
				{"/t/club", app.handleTally, nil},
				{"/t/club/players", app.handlePlayers, nil},
				{"/t/club/players/" + players[0].ID, app.handlePlayer, map[string]string{"pid": players[0].ID}},
				{"/t/club/periods", app.handlePeriods, nil},
				{"/t/club/periods/" + p.ID, app.handlePeriodDetail, map[string]string{"id": p.ID}},
				{"/t/club/periods/" + p.ID + "/edit", app.handleEditPeriodForm, map[string]string{"id": p.ID}},
				{"/t/club/play", app.handlePlay, nil},
				{"/t/club/rounds/" + rid + "?tab=poin", app.handleRound, map[string]string{"rid": rid}},
				{"/t/club/rounds/" + rid + "?tab=match", app.handleRound, map[string]string{"rid": rid}},
				{"/t/club/poster", app.handlePoster, nil},
				{"/t/club/settings", app.handleSettings, nil},
			} {
				r := httptest.NewRequest("GET", pg.path, nil)
				r.SetPathValue("slug", "club")
				for k, v := range pg.vals {
					r.SetPathValue(k, v)
				}
				r = r.WithContext(context.WithValue(r.Context(), userCtxKey, owner))
				w := httptest.NewRecorder()
				pg.h(w, r)
				if w.Code != 200 {
					t.Errorf("GET %s: %d %s", pg.path, w.Code, strings.TrimSpace(w.Body.String()))
				}
			}
		})
	}
}

// Phase 2: admins add, rename, reorder and delete leaderboards in Setelan →
// Papan; pages, forms and links stay on the leaderboard they show.
func TestLeaderboardsInApp(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	owner, _ := s.CreateUser(ctx, "owner", "x")
	fan, _ := s.CreateUser(ctx, "fan", "x")
	tl, _ := s.CreateTally(ctx, owner.ID, Tally{Slug: "club", Name: "Badminton Sampai Pingsan", Visibility: VisibilityPrivate}, Leaderboard{Template: "badminton_doubles"})
	app := &App{store: s, tmpl: buildTemplates(), aio: &AioAuth{}}
	do := func(u *User, method, path string, h http.HandlerFunc, vals map[string]string, form url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		if form != nil {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		r.SetPathValue("slug", "club")
		for k, v := range vals {
			r.SetPathValue(k, v)
		}
		if u != nil {
			r = r.WithContext(context.WithValue(r.Context(), userCtxKey, u))
		}
		w := httptest.NewRecorder()
		h(w, r)
		return w
	}
	get := func(u *User, path string, h http.HandlerFunc, vals map[string]string) string {
		t.Helper()
		w := do(u, "GET", path, h, vals, nil)
		if w.Code != 200 {
			t.Fatalf("GET %s: %d", path, w.Code)
		}
		return w.Body.String()
	}

	// One leaderboard: no switcher, plain links.
	if body := get(owner, "/t/club", app.handleTally, nil); strings.Contains(body, "?board=") || strings.Contains(body, "boardswitch") {
		t.Error("a tally with one leaderboard shows ?board= links or the switcher")
	}

	// Add "Basket" (team of 5) and a nameless counter.
	add := func(form url.Values) *httptest.ResponseRecorder {
		return do(owner, "POST", "/t/club/boards", app.handleCreateBoard, nil, form)
	}
	if w := add(url.Values{"name": {"Basket"}, "template": {"team"}, "side_size": {"5"}}); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/t/club/settings?ok=board_added#papan" {
		t.Fatalf("add leaderboard: %d %s", w.Code, w.Header().Get("Location"))
	}
	if w := add(url.Values{"template": {"team"}, "side_size": {"2"}}); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("team of 2: %d, want 422", w.Code)
	}
	add(url.Values{"template": {"counter"}})
	add(url.Values{"name": {"Basket"}, "template": {"team"}, "side_size": {"3"}})
	rows, _ := s.LeaderboardRows(ctx, tl.ID)
	var slugs []string
	for _, b := range rows {
		slugs = append(slugs, b.Slug)
	}
	if strings.Join(slugs, ",") != "badminton,basket,counter,basket-2" {
		t.Fatalf("slugs = %v", slugs)
	}
	basket, counter, basket2 := rows[1], rows[2], rows[3]

	// A season and a play day created on the Basket board land on it.
	w := do(owner, "POST", "/t/club/periods?board=basket", app.handleCreatePeriod, nil, url.Values{
		"name": {"Musim basket"}, "starts_on": {today().AddDate(0, 0, -7).Format(dateLayout)}, "ends_on": {today().AddDate(0, 2, 0).Format(dateLayout)},
		"format": {"outcome"}, "win_pts": {"3"}, "draw_pts": {"1"}, "loss_pts": {"0"}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/t/club/periods?board=basket&ok=period_created" {
		t.Fatalf("create season on basket: %d %s %s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	if ps, _ := s.ListPeriods(ctx, tl.ID, basket.ID); len(ps) != 1 {
		t.Fatalf("basket seasons = %v", ps)
	}
	if ps, _ := s.ListPeriods(ctx, tl.ID, rows[0].ID); len(ps) != 0 {
		t.Errorf("badminton got a season: %v", ps)
	}
	w = do(owner, "POST", "/t/club/rounds?board=basket", app.handleCreateRound, nil, url.Values{"played_on": {today().Format(dateLayout)}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("start play day on basket: %d %s", w.Code, w.Body.String())
	}
	if w := do(owner, "POST", "/t/club/rounds", app.handleCreateRound, nil, url.Values{"played_on": {today().Format(dateLayout)}}); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("play day on badminton, which has no season: %d, want 422", w.Code)
	}

	// Pages: switcher on board tabs, links keep ?board=, settings lists all.
	body := get(owner, "/t/club/play?board=basket", app.handlePlay, nil)
	for _, want := range []string{`class="boardswitch"`, `href="/t/club/play?board=badminton"`, `action="/t/club/rounds?board=basket"`, `href="/t/club/periods?board=basket"`, "Musim basket", ">Basket</a>"} {
		if !strings.Contains(body, want) {
			t.Errorf("basket Main tab: missing %q", want)
		}
	}
	if body := get(owner, "/t/club/players", app.handlePlayers, nil); strings.Contains(body, "boardswitch") {
		t.Error("the players tab shows the leaderboard switcher")
	}
	body = get(owner, "/t/club/settings", app.handleSettings, nil)
	for _, want := range []string{`id="papan"`, "?board=basket-2", "1 musim"} {
		if !strings.Contains(body, want) {
			t.Errorf("settings: missing %q", want)
		}
	}

	// Rename, reorder: moving Basket up makes it the default.
	action := func(id, act string, extra url.Values) *httptest.ResponseRecorder {
		form := url.Values{"action": {act}}
		for k, v := range extra {
			form[k] = v
		}
		return do(owner, "POST", "/t/club/boards/"+id, app.handleBoardAction, map[string]string{"id": id}, form)
	}
	action(counter.ID, "rename", url.Values{"name": {"Lari pagi"}})
	action(basket.ID, "up", nil)
	if b, _ := s.DefaultLeaderboard(ctx, tl.ID); b.ID != basket.ID {
		t.Errorf("default after moving Basket up: %s", b.Slug)
	}
	if body := get(owner, "/t/club", app.handleTally, nil); !strings.Contains(body, "Musim basket") {
		t.Error("/t/club doesn't show the new default leaderboard")
	}
	if body := get(owner, "/t/club?board=counter", app.handleTally, nil); !strings.Contains(body, "Lari pagi") {
		t.Error("renamed leaderboard's name isn't on the switcher")
	}

	// Delete: refused with seasons, allowed while empty.
	if w := action(basket.ID, "delete", nil); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("delete a leaderboard with a season: %d, want 422", w.Code)
	}
	if w := action(basket2.ID, "delete", nil); w.Code != http.StatusSeeOther {
		t.Errorf("delete an empty leaderboard: %d", w.Code)
	}
	if rows, _ := s.LeaderboardRows(ctx, tl.ID); len(rows) != 3 || rows[0].Position != 0 || rows[2].Position != 2 {
		t.Errorf("after delete: %+v", rows)
	}
	other, _ := s.CreateTally(ctx, owner.ID, Tally{Slug: "lain", Name: "Lain", Visibility: VisibilityLink}, Leaderboard{Template: "duel"})
	if err := s.DeleteLeaderboard(ctx, other.ID, owner.ID, defaultBoard(t, s, other.ID)); !errors.Is(err, ErrLastBoard) {
		t.Errorf("delete the only leaderboard: %v, want ErrLastBoard", err)
	}
	if w := action(defaultBoard(t, s, other.ID), "delete", nil); w.Code != http.StatusNotFound {
		t.Errorf("delete another tally's leaderboard through this one: %d, want 404", w.Code)
	}

	// Private stays private on every leaderboard; a linked player sees them.
	for _, p := range []string{"/t/club", "/t/club?board=basket", "/t/club?board=counter"} {
		if w := do(nil, "GET", p, app.handleTally, nil, nil); w.Code != http.StatusNotFound {
			t.Errorf("anonymous %s: %d, want 404", p, w.Code)
		}
	}

	// Mabar saya: one line per leaderboard with points this season.
	rd, _ := s.RoundByDate(ctx, tl.ID, basket.ID, today())
	if err := s.AddAwards(ctx, tl.ID, owner.ID, rd, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AddParticipants(ctx, tl.ID, owner.ID, []string{"Prabski"}); err != nil {
		t.Fatal(err)
	}
	players, _ := s.ListParticipants(ctx, tl.ID)
	s.pool.Exec(ctx, `UPDATE participants SET user_id = $1 WHERE id = $2`, fan.ID, players[0].ID)
	mine, err := s.MyPlayers(ctx, fan.ID)
	if err != nil || len(mine) != 1 || mine[0].Board == nil || mine[0].Board.ID != basket.ID {
		t.Fatalf("Mabar saya before points: %+v, %v (want the default leaderboard's line)", mine, err)
	}
	if err := s.AddAwards(ctx, tl.ID, owner.ID, rd, []AwardInput{{ParticipantID: players[0].ID, Points: 3}}, "Menang"); err != nil {
		t.Fatal(err)
	}
	if mine, _ := s.MyPlayers(ctx, fan.ID); len(mine) != 1 || mine[0].Rank != 1 || !strings.Contains(mine[0].Href(), "?season=") {
		t.Errorf("Mabar saya with points on basket: %+v", mine)
	}
	if body := get(fan, "/", app.handleHome, nil); !strings.Contains(body, "Badminton Sampai Pingsan &middot; Basket") {
		t.Error("home page doesn't name the leaderboard on the Mabar saya line")
	}
}
