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

const testTTL = 14 * 24 * time.Hour

// linkFixture is a private tally with two players and points for Ana, its
// owner, and two other accounts.
type linkFixture struct {
	s                 *Store
	tally             *Tally
	owner, ana, other *User
	anaID, budiID     string
}

func newLinkFixture(t *testing.T) linkFixture {
	t.Helper()
	s := testStore(t)
	ctx := context.Background()
	f := linkFixture{s: s}
	var err error
	if f.owner, err = s.CreateUser(ctx, "owner", "x"); err != nil {
		t.Fatal(err)
	}
	f.ana, _ = s.CreateUser(ctx, "ana_u", "x")
	f.other, _ = s.CreateUser(ctx, "other", "x")
	if f.tally, err = s.CreateTally(ctx, f.owner.ID, Tally{Slug: "rahasia", Name: "Rahasia", Template: "badminton_doubles", Visibility: VisibilityPrivate}); err != nil {
		t.Fatal(err)
	}
	d := today().AddDate(0, 0, -3)
	period, err := s.CreatePeriod(ctx, f.owner.ID, Period{TallyID: f.tally.ID, Name: "S1", StartsOn: d, EndsOn: d.AddDate(0, 2, 0), Scoring: DefaultContinuous})
	if err != nil {
		t.Fatal(err)
	}
	rid, _ := s.CreateRound(ctx, f.tally.ID, f.owner.ID, period.ID, d, 1, nil)
	rd, _ := s.RoundByID(ctx, f.tally.ID, rid)
	for _, p := range []struct {
		name string
		pts  int
	}{{"Ana", 42}, {"Budi", 30}} {
		if err := s.AddScore(ctx, f.tally.ID, f.owner.ID, rd, "", p.name, p.pts, 7); err != nil {
			t.Fatal(err)
		}
	}
	players, _ := s.ListParticipants(ctx, f.tally.ID)
	for _, p := range players {
		switch p.Name {
		case "Ana":
			f.anaID = p.ID
		case "Budi":
			f.budiID = p.ID
		}
	}
	return f
}

func pendingCount(t *testing.T, s *Store, participantID string) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM player_invites WHERE participant_id = $1 AND status = 'pending'`, participantID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPlayerInviteFlow(t *testing.T) {
	f := newLinkFixture(t)
	s, ctx, tid := f.s, context.Background(), f.tally.ID

	// A username nobody has is accepted the same way.
	if err := s.CreateInvite(ctx, tid, f.owner.ID, f.budiID, "nobody_here", testTTL, 30); err != nil {
		t.Fatalf("invite to an unknown username: %v", err)
	}
	// Inviting Ana twice keeps one pending invite, the latest.
	if err := s.CreateInvite(ctx, tid, f.owner.ID, f.anaID, "wrong_name", testTTL, 30); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateInvite(ctx, tid, f.owner.ID, f.anaID, "ana_u", testTTL, 30); err != nil {
		t.Fatal(err)
	}
	if n := pendingCount(t, s, f.anaID); n != 1 {
		t.Fatalf("pending invites for Ana = %d, want 1", n)
	}
	links, err := s.PlayerLinks(ctx, tid)
	if err != nil {
		t.Fatal(err)
	}
	if l := links[f.anaID]; l.Linked || l.Username != "ana_u" {
		t.Fatalf("Ana's link = %+v, want pending to ana_u", l)
	}

	// Only the invited username sees it, and only they can answer it.
	in, err := s.IncomingInvites(ctx, f.ana)
	if err != nil || len(in) != 1 || in[0].PlayerName != "Ana" || in[0].InvitedBy != "owner" {
		t.Fatalf("Ana's invites = %+v, %v", in, err)
	}
	if in, _ := s.IncomingInvites(ctx, f.other); len(in) != 0 {
		t.Fatalf("other sees %d invites", len(in))
	}
	if _, err := s.RespondInvite(ctx, f.other, in0(t, s, f.ana), InviteAccept); !errors.Is(err, ErrInviteGone) {
		t.Fatalf("someone else accepting: %v, want ErrInviteGone", err)
	}
	if _, err := s.RespondInvite(ctx, f.ana, in[0].ID, InviteAccept); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.LinkedPlayerID(ctx, tid, f.ana.ID); id != f.anaID {
		t.Fatalf("Ana's linked player = %q", id)
	}
	if _, err := s.RespondInvite(ctx, f.ana, in[0].ID, InviteAccept); !errors.Is(err, ErrInviteGone) {
		t.Fatalf("accepting twice: %v", err)
	}
	mine, err := s.MyPlayers(ctx, f.ana.ID)
	if err != nil || len(mine) != 1 || mine[0].Rank != 1 || mine[0].Of != 2 || mine[0].Points != 42 {
		t.Fatalf("Ana's players = %+v, %v", mine, err)
	}
	links, _ = s.PlayerLinks(ctx, tid)
	if l := links[f.anaID]; !l.Linked || l.Username != "ana_u" {
		t.Fatalf("after accepting, Ana's link = %+v", l)
	}

	// A linked player can't be invited; an account gets one player per tally.
	if err := s.CreateInvite(ctx, tid, f.owner.ID, f.anaID, "other", testTTL, 30); !errors.Is(err, ErrPlayerLinked) {
		t.Fatalf("inviting a linked player: %v", err)
	}
	if err := s.CreateInvite(ctx, tid, f.owner.ID, f.budiID, "ana_u", testTTL, 30); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RespondInvite(ctx, f.ana, in0(t, s, f.ana), InviteAccept); !errors.Is(err, ErrAlreadyMember) {
		t.Fatalf("second player in one tally: %v", err)
	}

	// Only the linked user can unlink themselves; admins can unlink anyone.
	if err := s.UnlinkPlayer(ctx, tid, f.other.ID, f.anaID, f.other.ID); !isNotFound(err) {
		t.Fatalf("someone else unlinking Ana: %v", err)
	}
	if err := s.UnlinkPlayer(ctx, tid, f.ana.ID, f.anaID, f.ana.ID); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.LinkedPlayerID(ctx, tid, f.ana.ID); id != "" {
		t.Fatalf("still linked to %q", id)
	}
}

// in0 is the id of the user's first open invite.
func in0(t *testing.T, s *Store, u *User) string {
	t.Helper()
	in, err := s.IncomingInvites(context.Background(), u)
	if err != nil || len(in) == 0 {
		t.Fatalf("no invite for %s: %v", u.Username, err)
	}
	return in[0].ID
}

func TestPlayerInviteMuteExpiryAndLimit(t *testing.T) {
	f := newLinkFixture(t)
	s, ctx, tid := f.s, context.Background(), f.tally.ID

	// "Abaikan tally ini" declines and hides later invites from the tally.
	if err := s.CreateInvite(ctx, tid, f.owner.ID, f.budiID, "other", testTTL, 30); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RespondInvite(ctx, f.other, in0(t, s, f.other), InviteMute); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateInvite(ctx, tid, f.owner.ID, f.budiID, "other", testTTL, 30); err != nil {
		t.Fatal(err)
	}
	if in, _ := s.IncomingInvites(ctx, f.other); len(in) != 0 {
		t.Fatalf("muted tally still shows %d invites", len(in))
	}
	// To the admins it still looks pending, like any unanswered invite.
	// So does a plain decline, until it expires.
	if links, _ := s.PlayerLinks(ctx, tid); links[f.budiID].Username != "other" {
		t.Fatalf("Budi's link = %+v", links[f.budiID])
	}
	if err := s.CreateInvite(ctx, tid, f.owner.ID, f.anaID, "ana_u", testTTL, 30); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RespondInvite(ctx, f.ana, in0(t, s, f.ana), InviteDecline); err != nil {
		t.Fatal(err)
	}
	if links, _ := s.PlayerLinks(ctx, tid); links[f.anaID].Username != "ana_u" || links[f.anaID].Linked {
		t.Fatalf("declined invite should look pending: %+v", links[f.anaID])
	}
	if err := s.CancelInvite(ctx, tid, f.owner.ID, f.anaID); err != nil {
		t.Fatal(err)
	}
	if links, _ := s.PlayerLinks(ctx, tid); links[f.anaID].Username != "" {
		t.Fatalf("cancelled declined invite still listed: %+v", links[f.anaID])
	}
	muted, _ := s.MutedTallies(ctx, f.other.ID)
	if len(muted) != 1 {
		t.Fatalf("muted = %+v", muted)
	}
	if err := s.Unmute(ctx, f.other.ID, tid); err != nil {
		t.Fatal(err)
	}
	if in, _ := s.IncomingInvites(ctx, f.other); len(in) != 1 {
		t.Fatalf("after unmuting, %d invites", len(in))
	}

	// An expired invite is gone for both sides.
	if err := s.CreateInvite(ctx, tid, f.owner.ID, f.anaID, "ana_u", -time.Minute, 30); err != nil {
		t.Fatal(err)
	}
	if in, _ := s.IncomingInvites(ctx, f.ana); len(in) != 0 {
		t.Fatalf("expired invite shown: %+v", in)
	}
	if links, _ := s.PlayerLinks(ctx, tid); links[f.anaID].Username != "" {
		t.Fatalf("expired invite still listed: %+v", links[f.anaID])
	}

	// The daily cap counts every invite sent in the last 24 hours.
	if err := s.CreateInvite(ctx, tid, f.owner.ID, f.anaID, "ana_u", testTTL, 4); !errors.Is(err, ErrInviteLimit) {
		t.Fatalf("over the cap: %v", err)
	}
	if err := s.CreateInvite(ctx, tid, f.owner.ID, f.anaID, "ana_u", testTTL, 5); err != nil {
		t.Fatalf("under the cap: %v", err)
	}
}

// The invite form answers the same for a username that exists and one that
// doesn't; linked players can read a private tally but not its admin pages.
func TestPlayerLinkPages(t *testing.T) {
	f := newLinkFixture(t)
	app := &App{store: f.s, tmpl: buildTemplates()}
	do := func(u *User, method string, h http.HandlerFunc, path string, form url.Values, vals map[string]string) *httptest.ResponseRecorder {
		var body *strings.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		} else {
			body = strings.NewReader("")
		}
		r := httptest.NewRequest(method, path, body)
		if form != nil {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
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
	slug := map[string]string{"slug": "rahasia"}

	send := func(username string) *httptest.ResponseRecorder {
		return do(f.owner, "POST", app.handleSendInvite, "/t/rahasia/invites", url.Values{"player": {"ana"}, "username": {username}}, slug)
	}
	known, unknown := send("@Ana_U"), send("no_such_user")
	if known.Code != http.StatusSeeOther || known.Code != unknown.Code || known.Header().Get("Location") != unknown.Header().Get("Location") {
		t.Fatalf("existing vs unknown username: %d %q vs %d %q", known.Code, known.Header().Get("Location"), unknown.Code, unknown.Header().Get("Location"))
	}
	if w := send("x"); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid username: %d", w.Code)
	}
	if w := do(f.owner, "POST", app.handleSendInvite, "/t/rahasia/invites", url.Values{"player": {"Nobody"}, "username": {"ana_u"}}, slug); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown player: %d", w.Code)
	}
	// Not an admin: the form isn't there.
	if w := do(f.ana, "POST", app.handleSendInvite, "/t/rahasia/invites", url.Values{"player": {"Ana"}, "username": {"ana_u"}}, slug); w.Code != http.StatusNotFound {
		t.Fatalf("non-admin sending an invite: %d", w.Code)
	}

	// The second send replaced the first, so re-invite Ana to her account.
	send("ana_u")
	home := do(f.ana, "GET", app.handleHome, "/", nil, nil)
	if home.Code != http.StatusOK || !strings.Contains(home.Body.String(), "/invites/") {
		t.Fatalf("home page without the invite: %d", home.Code)
	}
	w := do(f.ana, "POST", app.handleInviteAnswer, "/invites/x", url.Values{"answer": {"accept"}}, map[string]string{"id": in0(t, f.s, f.ana)})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("accept: %d %s", w.Code, w.Body.String())
	}
	home = do(f.ana, "GET", app.handleHome, "/", nil, nil)
	if body := home.Body.String(); !strings.Contains(body, "/t/rahasia/players/"+f.anaID) || strings.Contains(body, "/invites/") {
		t.Fatal("home page should list Ana under Mabar saya, with no invite left")
	}

	// Private tally: the linked player reads it; others and admin pages 404.
	if w := do(f.ana, "GET", app.handleTally, "/t/rahasia", nil, slug); w.Code != http.StatusOK {
		t.Fatalf("linked player on a private board: %d", w.Code)
	}
	player := do(f.ana, "GET", app.handlePlayer, "/t/rahasia/players/"+f.anaID, nil, map[string]string{"slug": "rahasia", "pid": f.anaID})
	if player.Code != http.StatusOK || !strings.Contains(player.Body.String(), "/links/"+f.anaID+"/unlink") {
		t.Fatalf("own player page: %d, want the unlink form", player.Code)
	}
	if w := do(f.other, "GET", app.handleTally, "/t/rahasia", nil, slug); w.Code != http.StatusNotFound {
		t.Fatalf("other user on a private board: %d", w.Code)
	}
	if w := do(f.ana, "GET", app.handlePlayers, "/t/rahasia/players", nil, slug); w.Code != http.StatusNotFound {
		t.Fatalf("linked player on the Pemain tab: %d", w.Code)
	}
	// Admins see the account on the Pemain tab.
	if w := do(f.owner, "GET", app.handlePlayers, "/t/rahasia/players", nil, slug); !strings.Contains(w.Body.String(), "@ana_u") {
		t.Fatal("Pemain tab doesn't show the linked account")
	}

	// Unlinking from the player page ends the private access.
	if w := do(f.ana, "POST", app.handleUnlinkSelf, "/links/x/unlink", url.Values{}, map[string]string{"pid": f.anaID}); w.Code != http.StatusSeeOther {
		t.Fatalf("unlink: %d", w.Code)
	}
	if w := do(f.ana, "GET", app.handleTally, "/t/rahasia", nil, slug); w.Code != http.StatusNotFound {
		t.Fatalf("after unlinking: %d", w.Code)
	}
}

func TestInviteSettings(t *testing.T) {
	t.Setenv("INVITE_TTL_DAYS", "")
	t.Setenv("INVITES_PER_TALLY_DAY", "")
	if ttl, n := inviteSettings(); ttl != 14*24*time.Hour || n != 30 {
		t.Fatalf("defaults = %v, %d", ttl, n)
	}
	t.Setenv("INVITE_TTL_DAYS", "7")
	t.Setenv("INVITES_PER_TALLY_DAY", "5")
	if ttl, n := inviteSettings(); ttl != 7*24*time.Hour || n != 5 {
		t.Fatalf("set = %v, %d", ttl, n)
	}
	t.Setenv("INVITE_TTL_DAYS", "0")
	t.Setenv("INVITES_PER_TALLY_DAY", "lots")
	if ttl, n := inviteSettings(); ttl != 14*24*time.Hour || n != 30 {
		t.Fatalf("invalid values = %v, %d", ttl, n)
	}
}
