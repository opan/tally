package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"
)

// AioAuth logs users in through all-in-one (OpenID Connect). It is used only
// when AUTH_PROVIDER=aio; otherwise tally keeps its own username/password
// login. Ported from cashflow's aioauth.go.
//
// Authentication fails closed: if aio is unreachable nobody new can log in.
// Existing tally sessions keep working, since every request after login uses
// tally's own session cookie.
type AioAuth struct {
	enabled       bool
	issuer        string
	clientID      string
	clientSecret  string
	redirectURL   string
	postLogoutURL string
	// internalURL is where server-to-server calls (the admin status check) go:
	// the cluster address when set, otherwise the public issuer.
	internalURL string
	httpClient  *http.Client

	mu            sync.Mutex
	oauth         oauth2.Config
	verifier      *oidc.IDTokenVerifier
	endSessionURL string
	ready         bool

	links *AioLinks
}

// AioLinks point to aio's own pages for forgetting and changing a password or
// email (RFC-003 §6.7). The forms stay on aio, so passwords are only ever
// typed there; client_id brands the pages as tally and return_to (checked by
// aio against the registered URIs) brings users back.
type AioLinks struct {
	Host     string // aio's address, named before users leave (RFC-003 §6.8)
	issuer   string
	clientID string
	self     string // tally's own origin
}

// newAioLinks takes tally's origin from the redirect URL that aio already has
// registered.
func newAioLinks(issuer, clientID, redirectURL string) *AioLinks {
	iu, err := url.Parse(issuer)
	if err != nil || iu.Host == "" {
		return nil
	}
	ru, err := url.Parse(redirectURL)
	if err != nil || ru.Host == "" {
		return nil
	}
	return &AioLinks{Host: iu.Host, issuer: issuer, clientID: clientID, self: ru.Scheme + "://" + ru.Host}
}

// ForgotURL and AccountURL open aio's pages in the user's language.
func (l *AioLinks) ForgotURL(lang string) string  { return l.link("/forgot-password", "/login", lang) }
func (l *AioLinks) AccountURL(lang string) string { return l.link("/account", "/akun", lang) }

func (l *AioLinks) link(page, returnTo, lang string) string {
	q := url.Values{"client_id": {l.clientID}, "return_to": {l.self + returnTo}, "ui_locales": {lang}}
	return l.issuer + page + "?" + q.Encode()
}

const (
	oidcStateCookie = "tally_oidc"
	oidcStateTTL    = 10 * time.Minute
)

var errUnlinkedAccount = errors.New("username belongs to a local tally account")

// NewAioAuthFromEnv reads AUTH_PROVIDER and the AIO_* settings. A half-done
// configuration is a startup error rather than a silent fallback, so a typo
// can't leave tally running without the login the operator expected.
func NewAioAuthFromEnv() (*AioAuth, error) {
	if strings.ToLower(strings.TrimSpace(env("AUTH_PROVIDER", "local"))) != "aio" {
		return &AioAuth{}, nil
	}
	a := &AioAuth{
		enabled:       true,
		issuer:        strings.TrimRight(strings.TrimSpace(env("AIO_ISSUER", "")), "/"),
		clientID:      strings.TrimSpace(env("AIO_CLIENT_ID", "")),
		clientSecret:  strings.TrimSpace(env("AIO_CLIENT_SECRET", "")),
		redirectURL:   strings.TrimSpace(env("AIO_REDIRECT_URL", "")),
		postLogoutURL: strings.TrimSpace(env("AIO_POST_LOGOUT_URL", "")),
		internalURL:   strings.TrimRight(strings.TrimSpace(env("AIO_INTERNAL_URL", "")), "/"),
		httpClient:    &http.Client{Timeout: aioStatusTimeout},
	}
	if a.internalURL == "" {
		a.internalURL = a.issuer
	}
	var missing []string
	for _, kv := range [][2]string{{"AIO_ISSUER", a.issuer}, {"AIO_CLIENT_ID", a.clientID},
		{"AIO_CLIENT_SECRET", a.clientSecret}, {"AIO_REDIRECT_URL", a.redirectURL}} {
		if kv[1] == "" {
			missing = append(missing, kv[0])
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("AUTH_PROVIDER=aio requires %s", strings.Join(missing, ", "))
	}
	a.links = newAioLinks(a.issuer, a.clientID, a.redirectURL)
	return a, nil
}

func (a *AioAuth) Enabled() bool { return a != nil && a.enabled }

// Links returns the account page links, nil when aio is disabled.
func (a *AioAuth) Links() *AioLinks {
	if !a.Enabled() {
		return nil
	}
	return a.links
}

// origin is aio's scheme://host[:port] ("" when disabled), for the CSP.
func (a *AioAuth) origin() string {
	if !a.Enabled() {
		return ""
	}
	u, err := url.Parse(a.issuer)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// discover fetches aio's discovery document and keys on first use. If aio is
// down it returns an error and is retried on the next login attempt, so tally
// can start (and serve logged-in users) while aio is unavailable. The network
// call runs outside the lock, so while aio is down each request waits for its
// own attempt rather than for everyone queued ahead of it.
func (a *AioAuth) discover(ctx context.Context) error {
	if a.isReady() {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	p, err := oidc.NewProvider(ctx, a.issuer)
	if err != nil {
		return fmt.Errorf("aio discovery: %w", err)
	}
	var extra struct {
		EndSession string `json:"end_session_endpoint"`
	}
	_ = p.Claims(&extra)

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ready {
		return nil
	}
	a.oauth = oauth2.Config{
		ClientID:     a.clientID,
		ClientSecret: a.clientSecret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  a.redirectURL,
		Scopes:       []string{oidc.ScopeOpenID, "profile"},
	}
	a.verifier = p.Verifier(&oidc.Config{ClientID: a.clientID})
	a.endSessionURL = extra.EndSession
	a.ready = true
	return nil
}

func (a *AioAuth) isReady() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ready
}

// loginState travels in a short-lived HttpOnly cookie between /auth/login and
// /auth/callback: the values the callback must match (state, nonce), the PKCE
// verifier, and where to land afterwards.
type loginState struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Next     string `json:"x,omitempty"`
}

func (st loginState) encode() string {
	b, _ := json.Marshal(st)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeLoginState(raw string) (loginState, error) {
	var st loginState
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return st, err
	}
	err = json.Unmarshal(b, &st)
	return st, err
}

// localUsername derives tally's username from aio's preferred_username,
// keeping tally's own rules (lowercase letters, digits, underscore, 3-30).
func localUsername(preferred, sub string) string {
	var b strings.Builder
	for _, r := range normalizeUsername(preferred) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	name := b.String()
	if len(name) > 30 {
		name = name[:30]
	}
	if len(name) < 3 {
		name = "user_" + shortID(sub)
	}
	return name
}

// suffixedUsername keeps two aio accounts that want the same name distinct,
// trimming the name so the result still fits tally's 30-character limit.
func suffixedUsername(name, sub string) string {
	id := shortID(sub)
	if max := 30 - 1 - len(id); len(name) > max {
		name = name[:max]
	}
	return name + "_" + id
}

func shortID(sub string) string {
	s := strings.ReplaceAll(sub, "-", "")
	if len(s) > 8 {
		s = s[:8]
	}
	return strings.ToLower(s)
}

func (a *App) setLoginStateCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: oidcStateCookie, Value: value, Path: "/auth", MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: isHTTPS(r),
	})
}

// authError shows a failed aio login and counts it by reason ("state",
// "denied", "unavailable", ...).
func (a *App) authError(w http.ResponseWriter, r *http.Request, status int, key string) {
	recordLogin(r.Context(), "aio", strings.TrimPrefix(key, "auth.err."))
	a.renderStatus(w, r, "autherror", authVM{Err: tr(r, key)}, status)
}

// handleAuthLogin starts a login (or, with ?signup=1, a registration) at aio.
func (a *App) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if !a.aio.Enabled() {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if currentUser(r) != nil {
		http.Redirect(w, r, nextOrHome(r.URL.Query().Get("next")), http.StatusSeeOther)
		return
	}
	if err := a.aio.discover(r.Context()); err != nil {
		log.Printf("aio login: %v", err)
		a.authError(w, r, http.StatusServiceUnavailable, "auth.err.unavailable")
		return
	}
	st := loginState{
		State: newToken(), Nonce: newToken(), Verifier: oauth2.GenerateVerifier(),
		Next: safeNext(r.URL.Query().Get("next")),
	}
	a.setLoginStateCookie(w, r, st.encode(), int(oidcStateTTL.Seconds()))

	opts := authCodeOptions(st, requestLang(r), r.URL.Query().Get("signup") == "1")
	http.Redirect(w, r, a.aio.oauth.AuthCodeURL(st.State, opts...), http.StatusFound)
}

// authCodeOptions are the authorize parameters besides state: PKCE, nonce,
// ui_locales so aio's login pages speak the language picked in tally, and
// prompt=create for "Daftar".
func authCodeOptions(st loginState, lang string, signup bool) []oauth2.AuthCodeOption {
	opts := []oauth2.AuthCodeOption{
		oauth2.S256ChallengeOption(st.Verifier),
		oidc.Nonce(st.Nonce),
		oauth2.SetAuthURLParam("ui_locales", lang),
	}
	if signup {
		opts = append(opts, oauth2.SetAuthURLParam("prompt", "create"))
	}
	return opts
}

// handleAuthCallback finishes a login: checks state, exchanges the code with
// the client secret and PKCE verifier, verifies the ID token (signature,
// issuer, audience, expiry, nonce), then logs the linked local user in.
func (a *App) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	if !a.aio.Enabled() {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	c, err := r.Cookie(oidcStateCookie)
	if err != nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}
	a.setLoginStateCookie(w, r, "", -1)
	st, err := decodeLoginState(c.Value)
	q := r.URL.Query()
	if err != nil || st.State == "" || q.Get("state") != st.State {
		a.authError(w, r, http.StatusBadRequest, "auth.err.state")
		return
	}
	if e := q.Get("error"); e != "" {
		log.Printf("aio login: aio returned %s: %s", e, q.Get("error_description"))
		a.authError(w, r, http.StatusBadRequest, "auth.err.denied")
		return
	}
	if err := a.aio.discover(r.Context()); err != nil {
		log.Printf("aio login: %v", err)
		a.authError(w, r, http.StatusServiceUnavailable, "auth.err.unavailable")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	tok, err := a.aio.oauth.Exchange(ctx, q.Get("code"), oauth2.VerifierOption(st.Verifier))
	if err != nil {
		log.Printf("aio login: code exchange: %v", err)
		a.authError(w, r, http.StatusBadGateway, "auth.err.exchange")
		return
	}
	rawID, _ := tok.Extra("id_token").(string)
	idt, err := a.aio.verifier.Verify(ctx, rawID)
	if err != nil || idt.Nonce != st.Nonce {
		log.Printf("aio login: id token rejected: %v (nonce match: %v)", err, idt != nil && idt.Nonce == st.Nonce)
		a.authError(w, r, http.StatusBadRequest, "auth.err.verify")
		return
	}
	var claims struct {
		PreferredUsername string `json:"preferred_username"`
	}
	_ = idt.Claims(&claims)

	u, err := a.linkedUser(ctx, idt.Subject, claims.PreferredUsername)
	if errors.Is(err, errUnlinkedAccount) {
		a.authError(w, r, http.StatusConflict, "auth.err.unlinked")
		return
	}
	if err != nil {
		internalError(w, "aio login: link user", err)
		return
	}
	if err := a.startSession(w, r, u.ID, rawID, a.aio.adminHint(ctx, idt.Subject)); err != nil {
		internalError(w, "aio login: start session", err)
		return
	}
	recordLogin(r.Context(), "aio", "ok")
	http.Redirect(w, r, nextOrHome(st.Next), http.StatusSeeOther)
}

// linkedUser returns the local user for an aio account, creating it on first
// login. A username that belongs to an existing local-only account is never
// taken over: that needs an explicit link.
func (a *App) linkedUser(ctx context.Context, sub, preferred string) (*User, error) {
	u, err := a.store.UserByAioID(ctx, sub)
	if err == nil {
		return u, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	name := localUsername(preferred, sub)
	u, err = a.store.CreateAioUser(ctx, sub, name)
	if err == nil {
		recordUserCreated(ctx, "aio")
	}
	if !errors.Is(err, ErrUsernameTaken) {
		return u, err
	}
	// The unique index that fired may be aio_user_id's: a simultaneous first
	// login for the same aio account created the user first.
	if u, err := a.store.UserByAioID(ctx, sub); err == nil {
		return u, nil
	}
	_, ownerAioID, err := a.store.UsernameOwner(ctx, name)
	if err != nil {
		return nil, err
	}
	if ownerAioID == "" {
		return nil, errUnlinkedAccount
	}
	// Taken by another aio account (e.g. a renamed one): keep both distinct.
	u, err = a.store.CreateAioUser(ctx, sub, suffixedUsername(name, sub))
	if err == nil {
		recordUserCreated(ctx, "aio")
	}
	if errors.Is(err, ErrUsernameTaken) {
		if winner, lookupErr := a.store.UserByAioID(ctx, sub); lookupErr == nil {
			return winner, nil
		}
	}
	return u, err
}

// logoutURL is where to send the browser after ending the local session, so
// aio ends its session too. Empty when there is nothing to tell aio.
func (a *AioAuth) logoutURL(idToken string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.ready || a.endSessionURL == "" || idToken == "" {
		return ""
	}
	q := url.Values{"id_token_hint": {idToken}, "client_id": {a.clientID}}
	if a.postLogoutURL != "" {
		q.Set("post_logout_redirect_uri", a.postLogoutURL)
	}
	return a.endSessionURL + "?" + q.Encode()
}
