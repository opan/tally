package main

import (
	"net/http"
	"sync"
	"time"
)

// submitGuard makes the forms that create records idempotent (ported from
// cashflow): each rendered form carries a one-time token (template func
// formToken), and only the first submission with a given token is processed.
// That defeats duplicates from a double tap, a second tab, or a resent POST on
// a slow court-side connection.
//
// A token is claimed before the work starts, so two concurrent submissions
// can't both pass, and released again if the work fails, so the user can retry
// the same form. State is in memory: a restart forgets claimed tokens, and with
// several replicas each pod has its own set. Both gaps are narrow (the same
// form would have to be resent across a restart, or land on another pod); a
// database-backed guard can close them if monitoring shows a need.
type submitGuard struct {
	mu   sync.Mutex
	seen map[string]time.Time
	ttl  time.Duration
}

// newSubmitGuard returns a guard that remembers claimed tokens for ttl and
// sweeps expired ones in the background. ttl only needs to outlast how long a
// form realistically stays open.
func newSubmitGuard(ttl time.Duration) *submitGuard {
	g := &submitGuard{seen: make(map[string]time.Time), ttl: ttl}
	go g.gc()
	return g
}

// claim records token and reports whether this is its first use. False means
// this is a duplicate submission. An empty token (a form rendered before the
// guard existed) is always allowed, rather than losing the user's input.
func (g *submitGuard) claim(token string) bool {
	if g == nil || token == "" {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.seen[token]; ok {
		return false
	}
	g.seen[token] = time.Now()
	return true
}

// release forgets a claimed token after the submission failed, so the same
// form can be sent again.
func (g *submitGuard) release(token string) {
	if g == nil || token == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.seen, token)
}

// claimSubmit claims the request's form token. For a duplicate it redirects to
// dupURL, where the first submission lands, and returns ok=false. Otherwise the
// caller sets *saved once the record is stored, and defers done: an unsaved
// submission releases its token so the form can be retried.
func (a *App) claimSubmit(w http.ResponseWriter, r *http.Request, dupURL string) (done func(saved *bool), ok bool) {
	token := r.FormValue("form_token")
	if !a.guard.claim(token) {
		http.Redirect(w, r, dupURL, http.StatusSeeOther)
		return nil, false
	}
	return func(saved *bool) {
		if !*saved {
			a.guard.release(token)
		}
	}, true
}

func (g *submitGuard) gc() {
	t := time.NewTicker(g.ttl)
	defer t.Stop()
	for range t.C {
		g.sweep(time.Now().Add(-g.ttl))
	}
}

func (g *submitGuard) sweep(cutoff time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for k, at := range g.seen {
		if at.Before(cutoff) {
			delete(g.seen, k)
		}
	}
}
