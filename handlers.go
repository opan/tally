package main

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

type App struct {
	store    *Store
	aio      *AioAuth // login through all-in-one (AUTH_PROVIDER=aio); nil-safe when disabled
	tmpl     map[string]*template.Template
	assetVer string       // content hash appended to static asset URLs for cache-busting
	guideVer string       // content hash of the guide's screenshots
	guard    *submitGuard // one-time form tokens against double submits; nil-safe
}

const maxFormBytes = 256 << 10 // 256 KiB cap for ordinary form posts

// limitBody caps a request body to guard against oversized posts (DoS).
func limitBody(w http.ResponseWriter, r *http.Request, max int64) {
	r.Body = http.MaxBytesReader(w, r.Body, max)
}

var pageNames = []string{"landing", "login", "register", "autherror", "panduan", "dashboard", "notfound", "tally", "players", "periods", "period_edit", "settings", "play", "round", "match_edit", "poster", "player"}

// pageExtras are additional template files a page needs.
var pageExtras = map[string][]string{
	"panduan": {"templates/guide_id.html", "templates/guide_en.html"},
}

var templateFuncs = template.FuncMap{
	// formToken is a fresh one-time token for forms guarded by submitGuard.
	"formToken": newToken,
	"add1":      func(i int) int { return i + 1 },
	// dict builds the argument map for a sub-template: dict "k1" v1 "k2" v2.
	"dict": func(kv ...any) (map[string]any, error) {
		if len(kv)%2 != 0 {
			return nil, errors.New("dict: odd number of arguments")
		}
		m := make(map[string]any, len(kv)/2)
		for i := 0; i < len(kv); i += 2 {
			k, ok := kv[i].(string)
			if !ok {
				return nil, errors.New("dict: key must be a string")
			}
			m[k] = kv[i+1]
		}
		return m, nil
	},
	"fmt1":      func(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) },
	"hasPrefix": strings.HasPrefix,
	// titleSize shrinks the poster title when its longer line won't fit.
	"titleSize": func(a, b string) string {
		n := max(utf8.RuneCountInString(a), utf8.RuneCountInString(b))
		switch {
		case n > 14:
			return "xlong"
		case n > 10:
			return "long"
		}
		return ""
	},
}

func buildTemplates() map[string]*template.Template {
	pages := map[string]*template.Template{}
	// Each page gets its own template set (layout + that page) so their
	// "content"/"title" blocks don't collide.
	for _, name := range pageNames {
		files := []string{"templates/layout.html", "templates/partials.html", "templates/" + name + ".html"}
		files = append(files, pageExtras[name]...)
		pages[name] = template.Must(template.New(name).Funcs(templateFuncs).ParseFS(tmplFS, files...))
	}
	return pages
}

// pageData wraps every page's view model with cross-cutting state. Templates
// reach strings through {{$.T "key"}}.
type pageData struct {
	Username string
	AssetVer string
	Lang     string
	Path     string // current path, for the language switcher's ?next=
	Data     any
}

func (p pageData) T(key string, args ...any) string { return translate(p.Lang, key, args...) }

func (p pageData) Date(t time.Time) string { return formatDate(p.Lang, t) }

// Rule is a one-line, human description of a points rule.
func (p pageData) Rule(c ScoringConfig) string {
	var s string
	switch c.Format {
	case FormatOutcome:
		return p.T("rule.outcome", c.WinPts, c.DrawPts, c.LossPts)
	case FormatCounter:
		return p.T("rule.counter")
	case FormatReset:
		s = p.T("rule.reset", c.Games, c.GameTarget)
	default:
		s = p.T("rule.continuous", c.Target)
	}
	if c.WinBonus > 0 {
		return s + " · " + p.T("rule.bonus", c.WinBonus)
	}
	return s + " · " + p.T("rule.no_bonus")
}

func (a *App) render(w http.ResponseWriter, r *http.Request, name string, data any) {
	a.renderStatus(w, r, name, data, http.StatusOK)
}

func (a *App) renderStatus(w http.ResponseWriter, r *http.Request, name string, data any, status int) {
	t, ok := a.tmpl[name]
	if !ok {
		http.Error(w, "template not found: "+name, http.StatusInternalServerError)
		return
	}
	pd := pageData{Data: data, AssetVer: a.assetVer, Lang: requestLang(r), Path: r.URL.RequestURI()}
	if u := currentUser(r); u != nil {
		pd.Username = u.Username
	}
	// Render into a buffer first so a template error becomes a clean 500
	// instead of a half-written response.
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", pd); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (a *App) notFound(w http.ResponseWriter, r *http.Request) {
	a.renderStatus(w, r, "notfound", nil, http.StatusNotFound)
}

func internalError(w http.ResponseWriter, what string, err error) {
	log.Printf("%s: %v", what, err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func (a *App) handleHome(w http.ResponseWriter, r *http.Request) {
	if currentUser(r) == nil {
		a.render(w, r, "landing", nil)
		return
	}
	a.renderDashboard(w, r, dashboardVM{}, http.StatusOK)
}

// --- User guide ---

type guideVM struct {
	Aio    bool   // login goes through All-in-one: the guide says so
	ImgVer string // cache-buster for the screenshots
}

func (a *App) handleGuide(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, "panduan", guideVM{Aio: a.aio.Enabled(), ImgVer: a.guideVer})
}

// --- Auth ---

type authVM struct {
	Err      string
	Username string
}

func (a *App) authFail(w http.ResponseWriter, r *http.Request, page, errKey, username string) {
	a.renderStatus(w, r, page, authVM{Err: translate(requestLang(r), errKey), Username: username}, http.StatusUnprocessableEntity)
}

func (a *App) handleRegisterForm(w http.ResponseWriter, r *http.Request) {
	if currentUser(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if a.aio.Enabled() {
		http.Redirect(w, r, "/auth/login?signup=1", http.StatusSeeOther)
		return
	}
	a.render(w, r, "register", authVM{})
}

func (a *App) handleRegister(w http.ResponseWriter, r *http.Request) {
	if a.aio.Enabled() {
		// Passwords live in all-in-one; local credentials are not accepted.
		http.Redirect(w, r, "/auth/login?signup=1", http.StatusSeeOther)
		return
	}
	limitBody(w, r, maxFormBytes)
	username := normalizeUsername(r.FormValue("username"))
	if key := validateUsername(username); key != "" {
		a.authFail(w, r, "register", key, username)
		return
	}
	pw := r.FormValue("password")
	if key := validatePassword(pw, r.FormValue("password2")); key != "" {
		a.authFail(w, r, "register", key, username)
		return
	}

	hash, err := hashPassword(pw)
	if err != nil {
		internalError(w, "hash password", err)
		return
	}
	u, err := a.store.CreateUser(r.Context(), username, hash)
	if errors.Is(err, ErrUsernameTaken) {
		a.authFail(w, r, "register", "err.username_taken", username)
		return
	}
	if err != nil {
		internalError(w, "create user", err)
		return
	}
	recordUserCreated(r.Context(), "local")
	if err := a.startSession(w, r, u.ID, ""); err != nil {
		internalError(w, "start session", err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if currentUser(r) != nil {
		http.Redirect(w, r, nextOrHome(r.URL.Query().Get("next")), http.StatusSeeOther)
		return
	}
	if a.aio.Enabled() {
		http.Redirect(w, r, aioLoginPath(r), http.StatusSeeOther)
		return
	}
	a.render(w, r, "login", authVM{})
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if a.aio.Enabled() {
		// Passwords live in all-in-one; local credentials are not accepted.
		http.Redirect(w, r, aioLoginPath(r), http.StatusSeeOther)
		return
	}
	limitBody(w, r, maxFormBytes)
	username := normalizeUsername(r.FormValue("username"))
	pw := r.FormValue("password")

	u, hash, err := a.store.UserByUsername(r.Context(), username)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !checkPassword(hash, pw)) {
		recordLogin(r.Context(), "local", "failed")
		a.authFail(w, r, "login", "err.login_failed", username)
		return
	}
	if err != nil {
		internalError(w, "login lookup", err)
		return
	}
	if err := a.startSession(w, r, u.ID, ""); err != nil {
		internalError(w, "start session", err)
		return
	}
	recordLogin(r.Context(), "local", "ok")
	http.Redirect(w, r, nextOrHome(r.URL.Query().Get("next")), http.StatusSeeOther)
}

// aioLoginPath is /auth/login, carrying a safe ?next= along.
func aioLoginPath(r *http.Request) string {
	if next := safeNext(r.URL.Query().Get("next")); next != "" {
		return "/auth/login?next=" + url.QueryEscape(next)
	}
	return "/auth/login"
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	limitBody(w, r, maxFormBytes)
	// For a session started through all-in-one, also end the aio session:
	// read its ID token before the local session (and the token) are deleted.
	var aioLogout string
	if a.aio.Enabled() {
		if c, err := r.Cookie(sessionCookie); err == nil {
			if idToken, err := a.store.SessionIDToken(r.Context(), c.Value); err == nil && idToken != "" {
				// After a restart nothing has run discovery yet; without it the
				// logout would end only tally's session and single sign-on would
				// log the user straight back in. If aio is down, fall back to
				// logging out of tally only.
				ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
				if err := a.aio.discover(ctx); err != nil {
					log.Printf("aio logout: %v", err)
				}
				cancel()
				aioLogout = a.aio.logoutURL(idToken)
			}
		}
	}
	a.endSession(w, r)
	if aioLogout != "" {
		http.Redirect(w, r, aioLogout, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
