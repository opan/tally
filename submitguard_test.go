package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSubmitGuard(t *testing.T) {
	g := &submitGuard{seen: map[string]time.Time{}, ttl: time.Hour}
	if !g.claim("a") || g.claim("a") {
		t.Fatal("a token must be claimable exactly once")
	}
	g.release("a")
	if !g.claim("a") {
		t.Error("a released token (failed submission) can be used again")
	}
	if !g.claim("") || !g.claim("") {
		t.Error("forms without a token are never blocked")
	}
	g.sweep(time.Now().Add(time.Minute))
	if !g.claim("a") {
		t.Error("expired tokens are swept")
	}
	var nilGuard *submitGuard
	if !nilGuard.claim("x") {
		t.Error("a nil guard allows everything")
	}
}

// Concurrent duplicates (double tap, two tabs) must let exactly one through.
func TestSubmitGuardConcurrent(t *testing.T) {
	g := &submitGuard{seen: map[string]time.Time{}, ttl: time.Hour}
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if g.claim("same") {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d submissions got through, want 1", wins)
	}
}

func TestClaimSubmit(t *testing.T) {
	app := &App{guard: &submitGuard{seen: map[string]time.Time{}, ttl: time.Hour}}
	post := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(url.Values{"form_token": {"tok"}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}

	// First submission fails validation: the token is released for a retry.
	done, ok := app.claimSubmit(httptest.NewRecorder(), post(), "/dup")
	if !ok {
		t.Fatal("first submission must go through")
	}
	saved := false
	done(&saved)

	// Retry succeeds and keeps the token.
	done, ok = app.claimSubmit(httptest.NewRecorder(), post(), "/dup")
	if !ok {
		t.Fatal("retry after a failed submission must go through")
	}
	saved = true
	done(&saved)

	// The duplicate lands where the first submission did.
	rr := httptest.NewRecorder()
	if _, ok := app.claimSubmit(rr, post(), "/dup"); ok {
		t.Fatal("duplicate must be stopped")
	}
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/dup" {
		t.Errorf("duplicate: %d → %q, want 303 → /dup", rr.Code, rr.Header().Get("Location"))
	}
}
