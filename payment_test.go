package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestParseRupiah(t *testing.T) {
	// One format: digits, grouped the way the field groups them per language.
	for _, c := range []struct {
		lang, in string
		want     int64
	}{
		{"id", "50000", 50000}, {"id", "50.000", 50000}, {"id", " 1.050.000 ", 1050000}, {"id", "0", 0},
		{"en", "50000", 50000}, {"en", "50,000", 50000}, {"en", "10,000,000", 10000000},
	} {
		if got, ok := parseRupiah(c.lang, c.in); !ok || got != c.want {
			t.Errorf("parseRupiah(%s, %q) = %d, %v; want %d", c.lang, c.in, got, ok, c.want)
		}
	}
	for _, c := range []struct{ lang, in string }{
		{"id", ""}, {"id", "abc"}, {"id", "-5"}, {"id", "Rp 50.000"}, {"id", "50rb"}, {"id", "50k"},
		{"id", "50.000,00"}, {"id", "50,000"}, {"id", "1.5"}, {"id", "50.00"}, {"id", "5.0000"},
		{"id", "50 000"}, {"id", "9999999999999"}, {"en", "50.000"}, {"en", "50,000.00"},
	} {
		if got, ok := parseRupiah(c.lang, c.in); ok {
			t.Errorf("parseRupiah(%s, %q) = %d, want an error", c.lang, c.in, got)
		}
	}
	if got := grouped("en", 1050000); got != "1,050,000" {
		t.Errorf("grouped(en) = %q", got)
	}
	for n, want := range map[int64]string{0: "Rp0", 500: "Rp500", 50000: "Rp50.000", 1050000: "Rp1.050.000"} {
		if got := rupiah("id", n); got != want {
			t.Errorf("rupiah(%d) = %q, want %q", n, got, want)
		}
	}
	if got := rupiah("en", 1050000); got != "Rp1,050,000" {
		t.Errorf("rupiah en = %q", got)
	}
}

// Iuran in the store: a fee per play day, one payment per player and day,
// undo, the history, and a player who paid can't be deleted.
func TestPaymentsStore(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, _ := s.CreateUser(ctx, "owner", "x")
	tl, err := s.CreateTally(ctx, u.ID, Tally{Slug: "club", Name: "Club", Visibility: VisibilityLink}, Leaderboard{Template: "badminton_doubles"})
	if err != nil {
		t.Fatal(err)
	}
	board := defaultBoard(t, s, tl.ID)
	s.AddParticipants(ctx, tl.ID, u.ID, []string{"Jacque Oui", "Prabski", "Bahlul"})
	ids := map[string]string{}
	players, _ := s.ListParticipants(ctx, tl.ID)
	for _, pl := range players {
		ids[pl.Name] = pl.ID
	}
	jacque, prabski := ids["Jacque Oui"], ids["Prabski"]
	p, _ := s.CreatePeriod(ctx, u.ID, Period{TallyID: tl.ID, LeaderboardID: board, Name: "S1", StartsOn: today().AddDate(0, -1, 0), EndsOn: today().AddDate(0, 1, 0), Scoring: DefaultContinuous})
	r1id, _ := s.CreateRound(ctx, tl.ID, u.ID, p.ID, today().AddDate(0, 0, -7), 1, nil)
	r2id, _ := s.CreateRound(ctx, tl.ID, u.ID, p.ID, today(), 1, nil)
	r1, _ := s.RoundByID(ctx, tl.ID, r1id)
	r2, _ := s.RoundByID(ctx, tl.ID, r2id)

	if ok, _ := s.HasFees(ctx, tl.ID); ok {
		t.Error("HasFees before any fee")
	}
	if _, err := s.RecordPayment(ctx, tl.ID, u.ID, r1, jacque, "", PayCash, ""); !errors.Is(err, ErrNoFee) {
		t.Fatalf("payment before a fee: %v, want ErrNoFee", err)
	}
	if fee, _ := s.SuggestedFee(ctx, board, r2); fee != 0 {
		t.Errorf("suggested fee with none set: %d", fee)
	}
	if err := s.SetRoundFee(ctx, tl.ID, u.ID, r1, 50000); err != nil {
		t.Fatal(err)
	}
	if fee, _ := s.SuggestedFee(ctx, board, r2); fee != 50000 {
		t.Errorf("suggested fee: %d, want the previous play day's 50000", fee)
	}
	if ok, _ := s.HasFees(ctx, tl.ID); !ok {
		t.Error("HasFees after setting one")
	}
	r1, _ = s.RoundByID(ctx, tl.ID, r1id)
	if r1.Fee != 50000 {
		t.Fatalf("fee after setting: %d", r1.Fee)
	}

	if _, err := s.RecordPayment(ctx, tl.ID, u.ID, r1, jacque, "", PayCash, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordPayment(ctx, tl.ID, u.ID, r1, prabski, "", PayFree, "pelatih"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordPayment(ctx, tl.ID, u.ID, r1, "", "Mas Gembul", PayTransfer, "BCA"); err != nil {
		t.Fatalf("payment with a new player: %v", err)
	}
	got, err := s.RecordPayment(ctx, tl.ID, u.ID, r1, jacque, "", PayTransfer, "")
	if !errors.Is(err, ErrPaid) || got.Method != PayCash || got.Name != "Jacque Oui" {
		t.Errorf("second payment: %v %+v, want ErrPaid with the cash payment", err, got)
	}
	if _, err := s.RecordPayment(ctx, tl.ID, u.ID, r1, "", "prabski", PayCash, ""); !errors.Is(err, ErrNameTaken) {
		t.Errorf("new player with a taken name: %v", err)
	}

	pays, _ := s.RoundPayments(ctx, r1.ID)
	sum := summarize(r1.Fee, pays)
	if len(pays) != 3 || pays[0].Name != "Mas Gembul" || sum.Cash != 1 || sum.Transfer != 1 || sum.Free != 1 || sum.Collected() != 100000 {
		t.Fatalf("payments: %+v sum %+v", pays, sum)
	}
	// Fixing the fee recalculates the day.
	if err := s.SetRoundFee(ctx, tl.ID, u.ID, r1, 40000); err != nil {
		t.Fatal(err)
	}
	days, _ := s.PaymentDays(ctx, tl.ID, board)
	if len(days) != 1 || days[0].Round.ID != r1.ID || days[0].Sum.Collected() != 80000 {
		t.Fatalf("payment days: %+v", days)
	}
	tot, _ := s.PaymentTotals(ctx, tl.ID)
	if tot.Days != 1 || tot.Sum.Count() != 3 || tot.Collected != 80000 {
		t.Errorf("totals: %+v", tot)
	}
	mine, _ := s.PlayerPayments(ctx, p.ID, prabski)
	if len(mine) != 1 || mine[0].Method != PayFree || mine[0].Note != "pelatih" {
		t.Errorf("player payments: %+v", mine)
	}
	all, _ := s.Totals(ctx)
	if all.Payments != 3 || all.Collected != 80000 {
		t.Errorf("site totals: %d payments, Rp%d", all.Payments, all.Collected)
	}

	if err := s.DeleteParticipant(ctx, tl.ID, u.ID, prabski); !errors.Is(err, ErrInUse) {
		t.Errorf("deleting a player who paid: %v, want ErrInUse", err)
	}
	if rid, err := s.UndoPayment(ctx, tl.ID, u.ID, pays[1].ID); err != nil || rid != r1.ID {
		t.Fatalf("undo: %s %v", rid, err)
	}
	if _, err := s.UndoPayment(ctx, tl.ID, u.ID, pays[1].ID); !isNotFound(err) {
		t.Errorf("undo twice: %v", err)
	}
	if err := s.DeleteParticipant(ctx, tl.ID, u.ID, prabski); err != nil {
		t.Errorf("deleting the player once the payment is undone: %v", err)
	}

	other, _ := s.CreateTally(ctx, u.ID, Tally{Slug: "other", Name: "Other", Visibility: VisibilityLink}, Leaderboard{Template: "badminton_doubles"})
	if _, err := s.UndoPayment(ctx, other.ID, u.ID, pays[0].ID); !isNotFound(err) {
		t.Errorf("undo from another tally: %v", err)
	}
	if _, err := s.RecordPayment(ctx, other.ID, u.ID, r1, jacque, "", PayCash, ""); !isNotFound(err) {
		t.Errorf("payment on another tally's play day: %v", err)
	}

	if err := s.SetPeriodArchived(ctx, tl.ID, u.ID, p.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRoundFee(ctx, tl.ID, u.ID, r1, 1); !errors.Is(err, ErrArchived) {
		t.Errorf("fee in an archived season: %v", err)
	}
	if _, err := s.UndoPayment(ctx, tl.ID, u.ID, pays[0].ID); !errors.Is(err, ErrArchived) {
		t.Errorf("undo in an archived season: %v", err)
	}

	var n int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tally_id = $1 AND entity = 'payment'`, tl.ID).Scan(&n)
	if n != 4 {
		t.Errorf("payment audit entries: %d, want 4 (3 recorded, 1 undone)", n)
	}
	s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tally_id = $1 AND entity = 'round' AND action = 'update'`, tl.ID).Scan(&n)
	if n != 2 {
		t.Errorf("fee audit entries: %d, want 2", n)
	}
}

// Iuran through the pages: the play day card, the Iuran page for everyone,
// notes for admins only, and private tallies stay private.
func TestPaymentsInApp(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	app := &App{store: s, tmpl: buildTemplates()}
	admin, _ := s.CreateUser(ctx, "owner", "x")
	tl, _ := s.CreateTally(ctx, admin.ID, Tally{Slug: "club", Name: "Club", Visibility: VisibilityLink}, Leaderboard{Template: "badminton_doubles"})
	s.AddParticipants(ctx, tl.ID, admin.ID, []string{"Jacque Oui", "Prabski", "Bahlul"})
	p, _ := s.CreatePeriod(ctx, admin.ID, Period{TallyID: tl.ID, LeaderboardID: defaultBoard(t, s, tl.ID), Name: "S1", StartsOn: today().AddDate(0, -1, 0), EndsOn: today().AddDate(0, 1, 0), Scoring: DefaultContinuous})
	rid, _ := s.CreateRound(ctx, tl.ID, admin.ID, p.ID, today(), 1, nil)

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
	day := map[string]string{"rid": rid}
	dayPage := "/t/club/rounds/" + rid

	// Before a fee: the card asks for one, and there's no Iuran tab or link.
	w := do(admin, "GET", dayPage, app.handleRound, day, nil)
	if body := w.Body.String(); !strings.Contains(body, `action="/t/club/rounds/`+rid+`/fee"`) || strings.Contains(body, `/payments"`) || strings.Contains(body, `href="/t/club/iuran"`) {
		t.Fatalf("play day without a fee should only offer the fee form")
	}
	if w := do(admin, "POST", "/", app.handleSetFee, day, url.Values{"fee": {"lima puluh"}}); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad fee: %d", w.Code)
	}
	if w := do(admin, "POST", "/", app.handleAddPayment, day, url.Values{"player": {"Prabski"}, "method": {PayCash}}); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("payment before a fee: %d", w.Code)
	}
	if w := do(admin, "POST", "/", app.handleSetFee, day, url.Values{"fee": {"50.000,00"}}); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("fee with decimals: %d", w.Code)
	}
	if w := do(admin, "POST", "/", app.handleSetFee, day, url.Values{"fee": {"50.000"}}); w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "ok=fee_saved") {
		t.Fatalf("set fee: %d %s", w.Code, w.Header().Get("Location"))
	}

	pay := func(player, method, note string, extra ...string) *httptest.ResponseRecorder {
		f := url.Values{"player": {player}, "method": {method}, "note": {note}}
		for i := 0; i+1 < len(extra); i += 2 {
			f.Set(extra[i], extra[i+1])
		}
		return do(admin, "POST", "/", app.handleAddPayment, day, f)
	}
	if w := pay("prabski", PayTransfer, "BCA a.n. istri"); w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "pay=transfer") {
		t.Fatalf("record transfer: %d %s", w.Code, w.Header().Get("Location"))
	}
	if w := pay("Jacque Oui", PayFree, "pelatih"); w.Code != http.StatusSeeOther {
		t.Fatalf("record free: %d", w.Code)
	}
	if w := pay("Prabski", PayCash, ""); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "sudah tercatat") {
		t.Errorf("paying twice: %d", w.Code)
	}
	if w := pay("Bahlul", "bitcoin", ""); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown method: %d", w.Code)
	}
	// A new name asks first, then adds the player.
	w = pay("Tante Smash", PayCash, "")
	if body := w.Body.String(); w.Code != 200 || !strings.Contains(body, `name="confirm_new" value="1"`) {
		t.Fatalf("new name should ask: %d", w.Code)
	}
	if w := pay("Tante Smash", PayCash, "", "confirm_new", "1"); w.Code != http.StatusSeeOther {
		t.Fatalf("confirmed new player: %d", w.Code)
	}

	w = do(admin, "GET", dayPage+"?ok=payment_saved", app.handleRound, day, nil)
	body := w.Body.String()
	for _, want := range []string{"Rp50.000 / orang", "3 tercatat", "1 tunai · 1 transfer · 1 gratis", "Rp100.000", "BCA a.n. istri", `/payments/`, `href="/t/club/iuran"`} {
		if !strings.Contains(body, want) {
			t.Errorf("play day page: missing %q", want)
		}
	}
	if !strings.Contains(body, `list="pay-players" maxlength="40" required autocomplete="off" autocapitalize="words" placeholder="`) || !strings.Contains(body, `<option value="Bahlul">`) || strings.Contains(body, `<datalist id="pay-players"><option value="Jacque Oui">`) {
		t.Errorf("the player field should offer only players not recorded yet")
	}

	// Everyone sees the history; notes are for admins.
	w = do(nil, "GET", "/t/club/iuran", app.handlePayments, nil, nil)
	if body := w.Body.String(); w.Code != 200 || !strings.Contains(body, "Prabski") || !strings.Contains(body, "Rp100.000") || strings.Contains(body, "BCA a.n. istri") || strings.Contains(body, "/payments/") {
		t.Errorf("public Iuran page: %d, want names and totals without notes or undo", w.Code)
	}
	if body := do(admin, "GET", "/t/club/iuran", app.handlePayments, nil, nil).Body.String(); !strings.Contains(body, "BCA a.n. istri") {
		t.Errorf("admins should see notes on the Iuran page")
	}
	if body := do(nil, "GET", "/t/club", app.handleTally, nil, nil).Body.String(); !strings.Contains(body, `href="/t/club/iuran"`) {
		t.Errorf("the public board should link to the Iuran page")
	}
	pid := ""
	players, _ := s.ListParticipants(ctx, tl.ID)
	for _, pl := range players {
		if pl.Name == "Prabski" {
			pid = pl.ID
		}
	}
	w = do(nil, "GET", "/", app.handlePlayer, map[string]string{"pid": pid}, nil)
	if body := w.Body.String(); !strings.Contains(body, `id="iuran"`) || !strings.Contains(body, "Transfer") || strings.Contains(body, "BCA a.n. istri") {
		t.Errorf("player page should list the payment without its note")
	}

	// Undo, then it's gone.
	pays, _ := s.RoundPayments(ctx, rid)
	if w := do(admin, "POST", "/", app.handleUndoPayment, map[string]string{"id": pays[0].ID}, url.Values{}); w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "ok=payment_undone") {
		t.Errorf("undo: %d %s", w.Code, w.Header().Get("Location"))
	}
	if after, _ := s.RoundPayments(ctx, rid); len(after) != 2 {
		t.Errorf("payments after undo: %d", len(after))
	}
	if w := do(admin, "POST", "/", app.handleSetFee, day, url.Values{"fee": {"0"}}); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("clearing the fee with payments: %d", w.Code)
	}

	// Private: nobody outside sees the Iuran page.
	if err := s.UpdateTally(ctx, admin.ID, tl, "Club", "", VisibilityPrivate); err != nil {
		t.Fatal(err)
	}
	if w := do(nil, "GET", "/t/club/iuran", app.handlePayments, nil, nil); w.Code != http.StatusNotFound {
		t.Errorf("private Iuran page for a visitor: %d, want 404", w.Code)
	}
}

// The demo data (scripts/demo-seed.sql) still fits the schema, and running
// it twice starts the club over.
func TestDemoSeed(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	seed, err := os.ReadFile("scripts/demo-seed.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := s.pool.Exec(ctx, string(seed)); err != nil {
			t.Fatal(err)
		}
	}
	tl, err := s.TallyBySlug(ctx, "sampai-pingsan")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.HasFees(ctx, tl.ID); !ok {
		t.Error("demo tally has no fees")
	}
	if tot, _ := s.PaymentTotals(ctx, tl.ID); tot.Days != 4 || tot.Sum.Count() < 40 {
		t.Errorf("demo payments: %+v", tot)
	}
	if u, hash, err := s.UserByUsername(ctx, "demo"); err != nil || u == nil || !checkPassword(hash, "demo1234") {
		t.Errorf("demo login: %v", err)
	}
}
