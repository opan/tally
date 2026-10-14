package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestLocalUsername(t *testing.T) {
	cases := []struct{ preferred, want string }{
		{"budi", "budi"},
		{"Budi", "budi"},
		{"budi.santoso", "budi_santoso"},
		{"  ana  ", "ana"},
		{"ab", "user_a1b2c3d4"},
		{"", "user_a1b2c3d4"},
		{strings.Repeat("x", 40), strings.Repeat("x", 30)},
	}
	for _, c := range cases {
		got := localUsername(c.preferred, "a1b2c3d4-e5f6-0000-0000-000000000000")
		if got != c.want {
			t.Errorf("localUsername(%q) = %q, want %q", c.preferred, got, c.want)
		}
		if key := validateUsername(got); key != "" {
			t.Errorf("localUsername(%q) = %q breaks tally's own rules: %s", c.preferred, got, key)
		}
	}
}

func TestSuffixedUsername(t *testing.T) {
	sub := "a1b2c3d4-e5f6-0000-0000-000000000000"
	if got := suffixedUsername("budi", sub); got != "budi_a1b2c3d4" {
		t.Errorf("short name: got %q", got)
	}
	got := suffixedUsername(strings.Repeat("x", 30), sub)
	if want := strings.Repeat("x", 21) + "_a1b2c3d4"; got != want {
		t.Errorf("long name: got %q, want %q", got, want)
	}
	if key := validateUsername(got); key != "" {
		t.Errorf("suffixed name %q breaks tally's own rules: %s", got, key)
	}
}

// While aio is down, each login must wait only for its own discovery
// attempt, not for every attempt queued ahead of it.
func TestDiscover_ConcurrentAttemptsDontQueue(t *testing.T) {
	const delay = 300 * time.Millisecond
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer slow.Close()
	a := &AioAuth{enabled: true, issuer: slow.URL}

	const n = 5
	start := time.Now()
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.discover(context.Background()); err == nil {
				t.Error("discovery against a failing aio must fail")
			}
		}()
	}
	wg.Wait()
	if elapsed := time.Since(start); elapsed > 3*delay {
		t.Errorf("%d concurrent attempts took %v; they are queueing behind one another", n, elapsed)
	}
}

func TestLoginStateRoundTrip(t *testing.T) {
	st := loginState{State: "s", Nonce: "n", Verifier: "v", Next: "/t/bala-bulu"}
	got, err := decodeLoginState(st.encode())
	if err != nil || got != st {
		t.Fatalf("round trip = %+v, %v; want %+v", got, err, st)
	}
	if _, err := decodeLoginState("not base64!"); err == nil {
		t.Error("garbage must not decode")
	}
}

func TestNewAioAuthFromEnv(t *testing.T) {
	t.Run("local by default", func(t *testing.T) {
		t.Setenv("AUTH_PROVIDER", "")
		a, err := NewAioAuthFromEnv()
		if err != nil || a.Enabled() {
			t.Fatalf("want disabled, got enabled=%v err=%v", a.Enabled(), err)
		}
	})
	t.Run("aio without settings is a startup error", func(t *testing.T) {
		t.Setenv("AUTH_PROVIDER", "aio")
		t.Setenv("AIO_ISSUER", "http://localhost:18080")
		_, err := NewAioAuthFromEnv()
		if err == nil || !strings.Contains(err.Error(), "AIO_CLIENT_ID, AIO_CLIENT_SECRET, AIO_REDIRECT_URL") {
			t.Fatalf("want error naming the missing settings, got %v", err)
		}
	})
	t.Run("aio fully configured", func(t *testing.T) {
		t.Setenv("AUTH_PROVIDER", "aio")
		t.Setenv("AIO_ISSUER", "http://localhost:18080/")
		t.Setenv("AIO_CLIENT_ID", "tally")
		t.Setenv("AIO_CLIENT_SECRET", "s")
		t.Setenv("AIO_REDIRECT_URL", "http://localhost:8090/auth/callback")
		a, err := NewAioAuthFromEnv()
		if err != nil || !a.Enabled() || a.issuer != "http://localhost:18080" {
			t.Fatalf("got enabled=%v issuer=%q err=%v", a.Enabled(), a.issuer, err)
		}
	})
}

func TestLogoutURL(t *testing.T) {
	a := &AioAuth{clientID: "tally", postLogoutURL: "http://localhost:8090/",
		ready: true, endSessionURL: "http://aio/api/v1/oauth2/end_session"}
	u, err := url.Parse(a.logoutURL("the-id-token"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("id_token_hint") != "the-id-token" || q.Get("post_logout_redirect_uri") != "http://localhost:8090/" || q.Get("client_id") != "tally" {
		t.Errorf("unexpected logout URL %s", u)
	}
	if a.logoutURL("") != "" {
		t.Error("no ID token (a local session) means nothing to tell aio")
	}
}

func TestSecurityHeaders_FormActionAllowsAioOnlyWhenEnabled(t *testing.T) {
	csp := func(a *AioAuth) string {
		rr := httptest.NewRecorder()
		securityHeaders(a.origin(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
			ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
		return rr.Header().Get("Content-Security-Policy")
	}
	if got := csp(&AioAuth{}); !strings.Contains(got, "form-action 'self';") {
		t.Errorf("local login keeps form-action 'self' only, got %q", got)
	}
	got := csp(&AioAuth{enabled: true, issuer: "https://auth.example.com"})
	if !strings.Contains(got, "form-action 'self' https://auth.example.com;") {
		t.Errorf("aio login must allow the logout redirect to aio, got %q", got)
	}
}

func TestAuthCodeOptions(t *testing.T) {
	st := loginState{State: "s", Nonce: "the-nonce", Verifier: oauth2.GenerateVerifier()}
	cfg := oauth2.Config{ClientID: "tally", Endpoint: oauth2.Endpoint{AuthURL: "https://aio.example/authorize"}}
	for _, lang := range []string{"id", "en"} {
		for _, signup := range []bool{false, true} {
			u, err := url.Parse(cfg.AuthCodeURL(st.State, authCodeOptions(st, lang, signup)...))
			if err != nil {
				t.Fatal(err)
			}
			q := u.Query()
			if q.Get("ui_locales") != lang {
				t.Errorf("ui_locales = %q, want %q (aio's pages in tally's language)", q.Get("ui_locales"), lang)
			}
			if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") != "the-nonce" {
				t.Errorf("missing PKCE or nonce: %s", u)
			}
			if got := q.Get("prompt") == "create"; got != signup {
				t.Errorf("signup=%v: prompt=%q", signup, q.Get("prompt"))
			}
		}
	}
}

// With AUTH_PROVIDER=aio the local password forms are never served or
// accepted; every entry point leads to /auth/login, keeping ?next=.
func TestLocalAuthRedirectsToAio(t *testing.T) {
	app := &App{aio: &AioAuth{enabled: true}}
	cases := []struct {
		method, target, want string
		h                    http.HandlerFunc
	}{
		{"GET", "/login?next=/t/bala", "/auth/login?next=%2Ft%2Fbala", app.handleLoginForm},
		{"GET", "/login?next=//evil.com", "/auth/login", app.handleLoginForm},
		{"POST", "/login", "/auth/login", app.handleLogin},
		{"GET", "/register", "/auth/login?signup=1", app.handleRegisterForm},
		{"POST", "/register", "/auth/login?signup=1", app.handleRegister},
	}
	for _, c := range cases {
		rr := httptest.NewRecorder()
		c.h(rr, httptest.NewRequest(c.method, c.target, nil))
		if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != c.want {
			t.Errorf("%s %s: %d → %q, want 303 → %q", c.method, c.target, rr.Code, rr.Header().Get("Location"), c.want)
		}
	}
}
