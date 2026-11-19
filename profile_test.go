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

// RFC-006 in the store: the level scale (default, add, rename, reorder,
// remove) keeps players in step, profiles are checked, and the queries
// public pages use never load a profile.
func TestPlayerProfileStore(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, _ := s.CreateUser(ctx, "owner", "x")
	tl, err := s.CreateTally(ctx, u.ID, Tally{Slug: "club", Name: "Club", Visibility: VisibilityLink}, Leaderboard{Template: "badminton_doubles"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AddParticipantsAs(ctx, tl.ID, u.ID, []string{"Jacque Oui", "Prabski"}, Member); err != nil {
		t.Fatal(err)
	}
	added, skipped, err := s.AddParticipantsAs(ctx, tl.ID, u.ID, []string{"Bahlul", "prabski"}, Guest)
	if err != nil || len(added) != 1 || len(skipped) != 1 {
		t.Fatalf("add as guest: %v %v %v", added, skipped, err)
	}
	if _, _, err := s.AddParticipantsAs(ctx, tl.ID, u.ID, []string{"Mas Gembul"}, "vip"); !errors.Is(err, ErrBadProfile) {
		t.Errorf("unknown status on add: %v", err)
	}
	roster := func() map[string]Participant {
		ps, err := s.ListRoster(ctx, tl.ID)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]Participant{}
		for _, p := range ps {
			m[p.Name] = p
		}
		return m
	}
	r := roster()
	if r["Prabski"].Membership != Member || r["Bahlul"].Membership != Guest {
		t.Errorf("membership on add: %+v", r)
	}
	jacque, prabski := r["Jacque Oui"].ID, r["Prabski"].ID

	// No levels yet: a level can't be set, but gender and status can.
	if err := s.SetParticipantProfile(ctx, tl.ID, u.ID, jacque, Profile{Level: "A"}); !errors.Is(err, ErrUnknownLevel) {
		t.Errorf("level before any exist: %v", err)
	}
	if err := s.SetParticipantProfile(ctx, tl.ID, u.ID, jacque, Profile{Gender: "x"}); !errors.Is(err, ErrBadProfile) {
		t.Errorf("unknown gender: %v", err)
	}
	if err := s.UseDefaultLevels(ctx, tl.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UseDefaultLevels(ctx, tl.ID, u.ID); !errors.Is(err, ErrLevelExists) {
		t.Errorf("default twice: %v", err)
	}
	if levels, _ := s.TallyLevels(ctx, tl.ID); strings.Join(levels, ",") != "A+,A,B+,B,C" {
		t.Errorf("default levels: %v", levels)
	}
	if err := s.AddLevel(ctx, tl.ID, u.ID, "b"); !errors.Is(err, ErrLevelExists) {
		t.Errorf("duplicate level (case): %v", err)
	}
	for _, p := range []struct {
		id string
		p  Profile
	}{{jacque, Profile{Level: "B", Gender: GenderMale, Membership: Member}}, {prabski, Profile{Level: "B", Gender: GenderFemale, Membership: Member}}} {
		if err := s.SetParticipantProfile(ctx, tl.ID, u.ID, p.id, p.p); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetParticipantProfile(ctx, tl.ID, u.ID, jacque, Profile{Level: "b"}); !errors.Is(err, ErrUnknownLevel) {
		t.Errorf("a level must match exactly: %v", err)
	}

	// Rename moves both players; a rename onto another level is refused.
	if n, err := s.RenameLevel(ctx, tl.ID, u.ID, "B", "B-"); err != nil || n != 2 {
		t.Fatalf("rename: %d %v", n, err)
	}
	if r := roster(); r["Jacque Oui"].Level != "B-" || r["Prabski"].Level != "B-" || r["Jacque Oui"].Gender != GenderMale {
		t.Errorf("after rename: %+v", r)
	}
	if _, err := s.RenameLevel(ctx, tl.ID, u.ID, "B-", "a"); !errors.Is(err, ErrLevelExists) {
		t.Errorf("rename onto another level: %v", err)
	}
	if _, err := s.RenameLevel(ctx, tl.ID, u.ID, "Z", "Y"); !isNotFound(err) {
		t.Errorf("rename a missing level: %v", err)
	}
	if err := s.MoveLevel(ctx, tl.ID, u.ID, "C", true); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveLevel(ctx, tl.ID, u.ID, "A+", true); err != nil { // already first: no change
		t.Fatal(err)
	}
	if levels, _ := s.TallyLevels(ctx, tl.ID); strings.Join(levels, ",") != "A+,A,B+,C,B-" {
		t.Errorf("after moves: %v", levels)
	}
	for _, l := range []string{"D", "E", "F", "G", "H"} {
		if err := s.AddLevel(ctx, tl.ID, u.ID, l); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddLevel(ctx, tl.ID, u.ID, "I"); !errors.Is(err, ErrTooManyLevels) {
		t.Errorf("an 11th level: %v", err)
	}

	// Removing a level clears it from its players and nothing else.
	if n, err := s.DeleteLevel(ctx, tl.ID, u.ID, "B-"); err != nil || n != 2 {
		t.Fatalf("delete level: %d %v", n, err)
	}
	if r := roster(); r["Jacque Oui"].Level != "" || r["Jacque Oui"].Membership != Member || r["Prabski"].Gender != GenderFemale {
		t.Errorf("after delete: %+v", r)
	}

	// Every change is audited; a level change says how many players moved.
	var profiles, levelEdits int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tally_id = $1 AND entity = 'participant' AND action = 'update'`, tl.ID).Scan(&profiles)
	s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tally_id = $1 AND entity = 'levels' AND (after->>'players')::int = 2`, tl.ID).Scan(&levelEdits)
	if profiles != 2 || levelEdits != 2 {
		t.Errorf("audit: %d profile changes (want 2), %d level edits with 2 players (want 2)", profiles, levelEdits)
	}

	// Public queries leave Profile empty.
	if err := s.SetParticipantProfile(ctx, tl.ID, u.ID, jacque, Profile{Level: "A", Gender: GenderMale, Membership: Guest}); err != nil {
		t.Fatal(err)
	}
	ps, _ := s.ListParticipants(ctx, tl.ID)
	for _, p := range ps {
		if p.Profile != (Profile{}) {
			t.Errorf("ListParticipants loaded %s's profile", p.Name)
		}
	}
	if p, _ := s.ParticipantByID(ctx, tl.ID, jacque); p.Profile != (Profile{}) {
		t.Errorf("ParticipantByID loaded a profile")
	}
	tot, _ := s.Totals(ctx)
	if tot.Members != 1 || tot.Guests != 2 {
		t.Errorf("totals: %d members, %d guests", tot.Members, tot.Guests)
	}
}

// RFC-006 in the app: the Pemain page shows and edits profiles, Setelan
// edits the levels, members can't do either, and no other page shows a
// level, gender or status (§5.4).
func TestPlayerProfileInApp(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const adminSub = "6f1c2a0e-8f4b-4f51-9a39-0d6f3d1e2b7a"
	aio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"data":{"admin":true,"blocked":false}}`))
	}))
	defer aio.Close()
	app := &App{store: s, tmpl: buildTemplates(),
		aio: &AioAuth{enabled: true, clientID: "tally", clientSecret: "x", internalURL: aio.URL, httpClient: &http.Client{Timeout: time.Second}}}
	owner, _ := s.CreateUser(ctx, "owner", "x")
	member, _ := s.CreateUser(ctx, "member", "x")
	siteAdmin, _ := s.CreateAioUser(ctx, adminSub, "boss")
	siteAdmin.AioUserID = adminSub
	tl, _ := s.CreateTally(ctx, owner.ID, Tally{Slug: "club", Name: "Club", Visibility: VisibilityLink}, Leaderboard{Template: "badminton_doubles"})

	do := func(user *User, method, path string, h http.HandlerFunc, vals map[string]string, form url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		if form != nil {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		r.SetPathValue("slug", "club")
		for k, v := range vals {
			r.SetPathValue(k, v)
		}
		if user != nil {
			r = r.WithContext(context.WithValue(r.Context(), userCtxKey, user))
		}
		w := httptest.NewRecorder()
		h(w, r)
		return w
	}
	post := func(user *User, h http.HandlerFunc, vals map[string]string, form url.Values) *httptest.ResponseRecorder {
		return do(user, "POST", "/", h, vals, form)
	}

	// Adding as guests, then the page without levels: no level picker, a
	// link to set them up.
	if w := post(owner, app.handleAddPlayers, nil, url.Values{"names": {"Jacque Oui\nPrabski"}, "membership": {Guest}}); w.Code != 200 {
		t.Fatalf("add as guests: %d", w.Code)
	}
	if w := post(owner, app.handleAddPlayers, nil, url.Values{"names": {"Bahlul"}, "membership": {"vip"}}); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("add with an unknown status: %d", w.Code)
	}
	body := do(owner, "GET", "/t/club/players", app.handlePlayers, nil, nil).Body.String()
	if strings.Contains(body, `name="level"`) && !strings.Contains(body, `type="hidden" name="level"`) || !strings.Contains(body, `href="/t/club/settings#level"`) {
		t.Errorf("Pemain without levels should hide the level picker and link to Setelan")
	}
	if strings.Count(body, `class="badge pbadge">Tamu<`) != 2 {
		t.Errorf("both guests should have a Tamu badge")
	}

	// Setelan → Level.
	levelAct := func(user *User, form url.Values) *httptest.ResponseRecorder {
		return post(user, app.handleLevelAction, nil, form)
	}
	if w := levelAct(member, url.Values{"action": {"default"}}); w.Code != http.StatusNotFound {
		t.Errorf("a member editing levels: %d, want 404", w.Code)
	}
	if w := levelAct(owner, url.Values{"action": {"default"}}); w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "ok=levels_added#level") {
		t.Fatalf("default levels: %d %s", w.Code, w.Header().Get("Location"))
	}
	for _, c := range []struct {
		form string
		want int
	}{
		{"action=add&name=", http.StatusUnprocessableEntity},
		{"action=add&name=Terlalu+panjang", http.StatusUnprocessableEntity},
		{"action=add&name=a%2B", http.StatusUnprocessableEntity}, // "a+" is A+
		{"action=add&name=++ZQ9++", http.StatusSeeOther},
		{"action=rename&level=Nope&name=X", http.StatusUnprocessableEntity},
		{"action=up&level=ZQ9", http.StatusSeeOther},
		{"action=bogus", http.StatusBadRequest},
	} {
		f, _ := url.ParseQuery(c.form)
		if w := levelAct(owner, f); w.Code != c.want {
			t.Errorf("level %s: %d, want %d", c.form, w.Code, c.want)
		}
	}
	if levels, _ := s.TallyLevels(ctx, tl.ID); strings.Join(levels, ",") != "A+,A,B+,B,ZQ9,C" {
		t.Errorf("levels: %v", levels)
	}

	// Edit a player's profile from Pemain.
	ps, _ := s.ListParticipants(ctx, tl.ID)
	ids := map[string]string{}
	for _, p := range ps {
		ids[p.Name] = p.ID
	}
	jacque := map[string]string{"id": ids["Jacque Oui"]}
	if w := post(member, app.handlePlayerAction, jacque, url.Values{"action": {"profile"}, "level": {"ZQ9"}}); w.Code != http.StatusNotFound {
		t.Errorf("a member editing a profile: %d, want 404", w.Code)
	}
	if w := post(owner, app.handlePlayerAction, jacque, url.Values{"action": {"profile"}, "level": {"Q"}}); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown level: %d", w.Code)
	}
	if w := post(owner, app.handlePlayerAction, jacque, url.Values{"action": {"profile"}, "level": {"ZQ9"}, "gender": {"f"}, "membership": {Guest}}); w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "ok=player_saved") {
		t.Fatalf("save profile: %d %s", w.Code, w.Header().Get("Location"))
	}
	body = do(owner, "GET", "/t/club/players", app.handlePlayers, nil, nil).Body.String()
	for _, want := range []string{`class="badge pbadge lvl">ZQ9<`, `title="Perempuan">P<`, `<option value="ZQ9" selected>ZQ9</option>`, `<option value="f" selected>`} {
		if !strings.Contains(body, want) {
			t.Errorf("Pemain: missing %q", want)
		}
	}
	body = do(owner, "GET", "/t/club/settings", app.handleSettings, nil, nil).Body.String()
	if !strings.Contains(body, `id="level"`) || !strings.Contains(body, "1 pemain punya level ZQ9") {
		t.Errorf("Setelan should list the levels and ask before removing one with players")
	}
	w := levelAct(owner, url.Values{"action": {"rename"}, "level": {"ZQ9"}, "name": {"ZQ8"}})
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "n=1") {
		t.Errorf("rename should say how many players moved: %s", loc)
	}
	r := httptest.NewRequest("GET", "/t/club/settings?n=1&ok=level_renamed", nil)
	if got := okNotice(r); got != "Level diganti; 1 pemain ikut diperbarui." {
		t.Errorf("notice: %q", got)
	}
	levelAct(owner, url.Values{"action": {"rename"}, "level": {"ZQ8"}, "name": {"ZQ9"}})

	// §5.4: give Jacque points, a play day and a payment, then check every
	// page that isn't Pemain or Setelan, for visitors and admins.
	p, _ := s.CreatePeriod(ctx, owner.ID, Period{TallyID: tl.ID, LeaderboardID: defaultBoard(t, s, tl.ID), Name: "S1", StartsOn: today().AddDate(0, -1, 0), EndsOn: today().AddDate(0, 1, 0), Scoring: DefaultContinuous})
	rid, _ := s.CreateRound(ctx, tl.ID, owner.ID, p.ID, today(), 1, nil)
	rd, _ := s.RoundByID(ctx, tl.ID, rid)
	if err := s.AddScore(ctx, tl.ID, owner.ID, rd, ids["Jacque Oui"], "", 21, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRoundFee(ctx, tl.ID, owner.ID, rd, 50000); err != nil {
		t.Fatal(err)
	}
	rd, _ = s.RoundByID(ctx, tl.ID, rid)
	if _, err := s.RecordPayment(ctx, tl.ID, owner.ID, rd, ids["Jacque Oui"], "", PayCash, ""); err != nil {
		t.Fatal(err)
	}
	pages := []struct {
		name string
		user *User
		h    http.HandlerFunc
		path string
		vals map[string]string
	}{
		{"Ringkasan", nil, app.handleTally, "/t/club", nil},
		{"Ringkasan (admin)", owner, app.handleTally, "/t/club", nil},
		{"play day", nil, app.handleTally, "/t/club?day=" + rid, nil},
		{"player", nil, app.handlePlayer, "/t/club/players/x", map[string]string{"pid": ids["Jacque Oui"]}},
		{"player (admin)", owner, app.handlePlayer, "/t/club/players/x", map[string]string{"pid": ids["Jacque Oui"]}},
		{"play day (admin)", owner, app.handleRound, "/t/club/rounds/x", map[string]string{"rid": rid}},
		{"Iuran", nil, app.handlePayments, "/t/club/iuran", nil},
		{"Iuran (admin)", owner, app.handlePayments, "/t/club/iuran", nil},
		{"poster", nil, app.handlePoster, "/t/club/poster", nil},
		{"site admin tally", siteAdmin, app.handleAdminTally, "/admin/x", map[string]string{"id": tl.ID}},
		{"site admin player", siteAdmin, app.handleAdminPlayer, "/admin/x", map[string]string{"id": tl.ID, "pid": ids["Jacque Oui"]}},
		{"site admin day", siteAdmin, app.handleAdminDay, "/admin/x", map[string]string{"id": tl.ID, "rid": rid}},
	}
	for _, pg := range pages {
		w := do(pg.user, "GET", pg.path, pg.h, pg.vals, nil)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, "Jacque Oui") {
			t.Errorf("%s: %d, want 200 listing Jacque Oui", pg.name, w.Code)
			continue
		}
		for _, bad := range []string{"ZQ9", "pbadge", "Tamu", "Perempuan"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s shows %q", pg.name, bad)
			}
		}
	}
	if body := do(owner, "GET", "/t/club/play", app.handlePlay, nil, nil).Body.String(); strings.Contains(body, "ZQ9") || strings.Contains(body, "pbadge") {
		t.Errorf("Main shows a profile")
	}
}
