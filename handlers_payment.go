package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// Iuran (RFC-004): a fee per play day, payments recorded one at a time on the
// play day page, and the history for everyone on the Iuran page.

const maxPayNoteLen = 80

// parseRupiah reads a fee as typed: "50000", "50.000", "Rp 50.000", "50rb"
// or "50k". ok is false for anything else.
func parseRupiah(s string) (int64, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "rp")
	mult := int64(1)
	for _, suf := range []string{"rb", "k"} {
		if strings.HasSuffix(s, suf) {
			s, mult = strings.TrimSuffix(s, suf), 1000
			break
		}
	}
	s = strings.NewReplacer(".", "", ",", "", " ", "").Replace(s)
	if s == "" || len(s) > 12 {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n * mult, true
}

// payVM is the Iuran card on the play day page.
type payVM struct {
	Fee        int64
	FeeField   string // the fee form's value: as typed, else the fee, else a suggestion
	EditFee    bool   // open the fee form after a failed change
	Payments   []Payment
	Sum        PaySummary
	Names      []string // active players not recorded yet, for the player field
	Methods    []string
	FPlayer    string
	FMethod    string
	FNote      string
	ConfirmNew string // typed name that isn't a player yet; asks before adding
	Err        string // the card's own messages: autofocus scrolls the page to it,
	Notice     string // away from the alerts at the top
}

// fillPay sets up the Iuran card. The method picked last stays picked (?pay=).
func (a *App) fillPay(r *http.Request, d *roundData) payVM {
	pv := payVM{Fee: d.round.Fee, Payments: d.payments, Sum: summarize(d.round.Fee, d.payments), Methods: payMethods, FMethod: PayCash}
	if m := r.URL.Query().Get("pay"); m == PayTransfer {
		pv.FMethod = m
	}
	paid := map[string]bool{}
	for _, p := range d.payments {
		paid[p.ParticipantID] = true
	}
	for _, p := range d.players {
		if p.Active && !paid[p.ID] {
			pv.Names = append(pv.Names, p.Name)
		}
	}
	fee := d.round.Fee
	if fee == 0 {
		if s, err := a.store.SuggestedFee(r.Context(), d.tc.Board.ID, d.round); err == nil {
			fee = s
		}
	}
	if fee > 0 {
		pv.FeeField = strconv.FormatInt(fee, 10)
	}
	return pv
}

func (a *App) handleSetFee(w http.ResponseWriter, r *http.Request) {
	tc, u, ok := a.adminTally(w, r, "play")
	if !ok {
		return
	}
	limitBody(w, r, maxFormBytes)
	d, ok := a.editableRound(w, r, tc, r.PathValue("rid"))
	if !ok {
		return
	}
	typed := strings.TrimSpace(r.FormValue("fee"))
	fail := func(key string, args ...any) {
		d.tc.Err = tr(r, key, args...)
		a.renderRoundVM(w, r, d, nil, func(vm *roundVM) {
			vm.Pay.FeeField, vm.Pay.EditFee, vm.Focus = typed, true, "fee"
			vm.Pay.Err, vm.Err = vm.Err, "" // shown in the card, where the page scrolls to
		}, http.StatusUnprocessableEntity)
	}
	fee, ok := parseRupiah(typed)
	switch {
	case !ok || fee > maxFee:
		fail("err.fee", rupiah("id", maxFee))
		return
	case fee == 0 && len(d.payments) > 0:
		fail("err.fee_in_use")
		return
	}
	switch err := a.store.SetRoundFee(r.Context(), tc.Tally.ID, u.ID, d.round, fee); {
	case errors.Is(err, ErrArchived):
		http.Redirect(w, r, roundPath(tc.Tally, d.round.ID), http.StatusSeeOther)
		return
	case err != nil:
		internalError(w, "set fee", err)
		return
	}
	if fee != d.round.Fee {
		recordPaymentEvent(r.Context(), "fee_set")
	}
	redirectOK(w, r, roundPath(tc.Tally, d.round.ID), "fee_saved")
}

// handleAddPayment records that one player, picked by name, paid the play
// day's fee. A name that isn't a player yet is added only after the admin
// confirms (confirm_new=1), as on the Poin tab.
func (a *App) handleAddPayment(w http.ResponseWriter, r *http.Request) {
	tc, u, ok := a.adminTally(w, r, "play")
	if !ok {
		return
	}
	limitBody(w, r, maxFormBytes)
	d, ok := a.editableRound(w, r, tc, r.PathValue("rid"))
	if !ok {
		return
	}
	name := strings.Join(strings.Fields(r.FormValue("player")), " ")
	method := r.FormValue("method")
	note := strings.Join(strings.Fields(r.FormValue("note")), " ")
	back := roundPath(tc.Tally, d.round.ID) + "?ok=payment_saved" // no #fragment: it would stop autofocus
	if method == PayTransfer {
		back += "&pay=" + method
	}
	done, ok := a.claimSubmit(w, r, back)
	if !ok {
		return
	}
	saved := false
	defer done(&saved)

	fail := func(status int, tweak func(*roundVM)) {
		a.renderRoundVM(w, r, d, nil, func(vm *roundVM) {
			vm.Pay.FPlayer, vm.Pay.FNote, vm.Focus = name, note, "payer"
			vm.Pay.Err, vm.Err = vm.Err, "" // shown in the card, where the page scrolls to
			if validPayMethod(method) {
				vm.Pay.FMethod = method
			}
			if tweak != nil {
				tweak(vm)
			}
		}, status)
	}
	refuse := func(key string, args ...any) {
		d.tc.Err = tr(r, key, args...)
		fail(http.StatusUnprocessableEntity, nil)
	}
	switch {
	case d.round.Fee <= 0:
		d.tc.Err = tr(r, "err.pay_no_fee")
		fail(http.StatusUnprocessableEntity, func(vm *roundVM) { vm.Focus = "fee" })
		return
	case name == "":
		refuse("err.score_player")
		return
	case tooLong(name, maxPlayerNameLen):
		refuse("err.player_name_long", name, maxPlayerNameLen)
		return
	case !validPayMethod(method):
		refuse("err.pay_method")
		return
	case tooLong(note, maxPayNoteLen):
		refuse("err.pay_note_long", maxPayNoteLen)
		return
	}

	var playerID, newName string
	for _, p := range d.players {
		if strings.EqualFold(p.Name, name) {
			playerID, name = p.ID, p.Name
		}
	}
	if playerID == "" {
		if r.FormValue("confirm_new") != "1" {
			fail(http.StatusOK, func(vm *roundVM) { vm.Pay.ConfirmNew = name })
			return
		}
		newName = name
	}
	got, err := a.store.RecordPayment(r.Context(), tc.Tally.ID, u.ID, d.round, playerID, newName, method, note)
	switch {
	case errors.Is(err, ErrPaid):
		d.tc.Err = tr(r, "err.pay_already", name, tr(r, "pay.method."+got.Method), clock(requestLang(r), got.CreatedAt))
		fail(http.StatusUnprocessableEntity, func(vm *roundVM) { vm.Pay.FPlayer, vm.Pay.FNote = "", "" })
		return
	case errors.Is(err, ErrNameTaken):
		d.tc.Err = tr(r, "err.score_retry")
		fail(http.StatusConflict, nil)
		return
	case errors.Is(err, ErrNoFee):
		d.tc.Err = tr(r, "err.pay_no_fee")
		fail(http.StatusUnprocessableEntity, func(vm *roundVM) { vm.Focus = "fee" })
		return
	case errors.Is(err, ErrArchived):
		http.Redirect(w, r, roundPath(tc.Tally, d.round.ID), http.StatusSeeOther)
		return
	case err != nil:
		internalError(w, "record payment", err)
		return
	}
	saved = true
	if newName != "" {
		recordPlayersAdded(r.Context(), 1)
	}
	recordPayment(r.Context(), method)
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (a *App) handleUndoPayment(w http.ResponseWriter, r *http.Request) {
	tc, u, ok := a.adminTally(w, r, "play")
	if !ok {
		return
	}
	roundID, err := a.store.UndoPayment(r.Context(), tc.Tally.ID, u.ID, r.PathValue("id"))
	switch {
	case isNotFound(err):
		a.notFound(w, r)
		return
	case errors.Is(err, ErrArchived):
		http.Redirect(w, r, roundPath(tc.Tally, roundID), http.StatusSeeOther)
		return
	case err != nil:
		internalError(w, "undo payment", err)
		return
	}
	recordPaymentEvent(r.Context(), "undone")
	redirectOK(w, r, roundPath(tc.Tally, roundID), "payment_undone")
}

// paymentsVM is the Iuran page: every play day of the leaderboard that has a
// fee, newest first.
type paymentsVM struct {
	tallyCtx
	Days      []PayDay
	Sum       PaySummary // Fee unset: days' fees differ
	Collected int64
}

// handlePayments is the Iuran page, for everyone who can see the tally.
// Notes are for admins only: they can hold things like whose account paid.
func (a *App) handlePayments(w http.ResponseWriter, r *http.Request) {
	tc, ok := a.loadTally(w, r, "iuran")
	if !ok {
		return
	}
	days, err := a.store.PaymentDays(r.Context(), tc.Tally.ID, tc.Board.ID)
	if err != nil {
		internalError(w, "payment days", err)
		return
	}
	vm := paymentsVM{tallyCtx: tc, Days: days}
	for _, d := range days {
		vm.Sum.add(d.Sum)
		vm.Collected += d.Sum.Collected()
	}
	a.render(w, r, "payments", vm)
}

// playerPayment is a payment on a player's page, with its play day.
type playerPayment struct {
	Round Round
	Payment
}

// rupiah renders "Rp50.000" (id) or "Rp50,000" (en).
func rupiah(lang string, n int64) string {
	sep := "."
	if lang == "en" {
		sep = ","
	}
	neg := n < 0
	if neg {
		n = -n
	}
	digits := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, c := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteString(sep)
		}
		b.WriteRune(c)
	}
	if neg {
		return "-Rp" + b.String()
	}
	return "Rp" + b.String()
}
