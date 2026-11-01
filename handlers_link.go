package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Linking players to accounts (RFC-002): invites from the Pemain tab, answers
// and "Mabar saya" on the home page.

const (
	defaultInviteTTLDays  = 14
	defaultInvitesPerDay  = 30
	maxInviteTTLDays      = 365
	maxInvitesPerTallyDay = 10000
)

// inviteSettings reads INVITE_TTL_DAYS and INVITES_PER_TALLY_DAY. They're site
// settings, not tally settings: the cap protects users from a tally's admins.
// A value that isn't a positive number falls back to the default.
func inviteSettings() (ttl time.Duration, perDay int) {
	num := func(key string, def, max int) int {
		n, err := strconv.Atoi(env(key, ""))
		if err != nil || n < 1 || n > max {
			return def
		}
		return n
	}
	return time.Duration(num("INVITE_TTL_DAYS", defaultInviteTTLDays, maxInviteTTLDays)) * 24 * time.Hour,
		num("INVITES_PER_TALLY_DAY", defaultInvitesPerDay, maxInvitesPerTallyDay)
}

// inviteLimits are the App's invite settings, with the defaults for an App
// built without them (tests).
func (a *App) inviteLimits() (time.Duration, int) {
	ttl, perDay := a.inviteTTL, a.invitesPerDay
	if ttl <= 0 {
		ttl = defaultInviteTTLDays * 24 * time.Hour
	}
	if perDay <= 0 {
		perDay = defaultInvitesPerDay
	}
	return ttl, perDay
}

// handleSendInvite invites an account, by username, to be one of the tally's
// players. Whatever the username, a valid form gets the same answer: the
// username is not looked up (RFC-002 §5.1).
func (a *App) handleSendInvite(w http.ResponseWriter, r *http.Request) {
	tc, u, ok := a.adminTally(w, r, "players")
	if !ok {
		return
	}
	limitBody(w, r, maxFormBytes)
	back := tallyPath(tc.Tally, "/players")
	done, ok := a.claimSubmit(w, r, back)
	if !ok {
		return
	}
	saved := false
	defer done(&saved)

	name := strings.Join(strings.Fields(r.FormValue("player")), " ")
	username := normalizeUsername(strings.TrimPrefix(strings.TrimSpace(r.FormValue("username")), "@"))
	fail := func(key string, args ...any) {
		tc.Err = tr(r, key, args...)
		a.renderPlayers(w, r, playersVM{tallyCtx: tc, FLinkPlayer: name, FLinkUser: username, OpenLink: true}, http.StatusUnprocessableEntity)
	}
	players, err := a.store.ListParticipants(r.Context(), tc.Tally.ID)
	if err != nil {
		internalError(w, "list players", err)
		return
	}
	var playerID string
	for _, p := range players {
		if strings.EqualFold(p.Name, name) {
			playerID = p.ID
		}
	}
	if playerID == "" {
		fail("err.invite_player")
		return
	}
	if key := validateUsername(username); key != "" {
		fail(key)
		return
	}
	ttl, perDay := a.inviteLimits()
	err = a.store.CreateInvite(r.Context(), tc.Tally.ID, u.ID, playerID, username, ttl, perDay)
	switch {
	case errors.Is(err, ErrInviteLimit):
		fail("err.invite_limit", perDay)
	case errors.Is(err, ErrPlayerLinked):
		fail("err.player_linked")
	case isNotFound(err):
		fail("err.invite_player")
	case err != nil:
		internalError(w, "send invite", err)
	default:
		saved = true
		recordInviteEvent(r.Context(), "sent")
		redirectOK(w, r, back, "invite_sent")
	}
}

// --- Home page ---

// handleInviteAnswer accepts, declines or ignores one of the user's invites.
func (a *App) handleInviteAnswer(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	limitBody(w, r, maxFormBytes)
	answer := r.FormValue("answer")
	if answer != InviteAccept && answer != InviteDecline && answer != InviteMute {
		http.Error(w, "unknown answer", http.StatusBadRequest)
		return
	}
	_, err := a.store.RespondInvite(r.Context(), u, r.PathValue("id"), answer)
	switch {
	case errors.Is(err, ErrInviteGone):
		a.renderDashboard(w, r, dashboardVM{InviteErr: tr(r, "err.invite_gone")}, http.StatusConflict)
	case errors.Is(err, ErrAlreadyMember):
		a.renderDashboard(w, r, dashboardVM{InviteErr: tr(r, "err.already_member")}, http.StatusConflict)
	case err != nil:
		internalError(w, "answer invite", err)
	default:
		recordInviteEvent(r.Context(), map[string]string{InviteAccept: "accepted", InviteDecline: "declined", InviteMute: "ignored"}[answer])
		redirectOK(w, r, "/", "invite_"+answer)
	}
}

// handleUnmute shows a tally's invites to the user again.
func (a *App) handleUnmute(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	limitBody(w, r, maxFormBytes)
	if err := a.store.Unmute(r.Context(), u.ID, r.PathValue("tid")); err != nil {
		internalError(w, "unmute", err)
		return
	}
	redirectOK(w, r, "/", "unmuted")
}

// handleUnlinkSelf lets a linked player remove the link to their account.
func (a *App) handleUnlinkSelf(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	limitBody(w, r, maxFormBytes)
	tallyID, err := a.store.TallyOfMyPlayer(r.Context(), u.ID, r.PathValue("pid"))
	if err == nil {
		err = a.store.UnlinkPlayer(r.Context(), tallyID, u.ID, r.PathValue("pid"), u.ID)
	}
	switch {
	case isNotFound(err):
		a.notFound(w, r)
	case err != nil:
		internalError(w, "unlink player", err)
	default:
		recordInviteEvent(r.Context(), "unlinked")
		redirectOK(w, r, "/", "link_removed")
	}
}
