package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
)

//go:embed locales/*.json
var localeFS embed.FS

const (
	defaultLang = "id"
	langCookie  = "tally_lang"
)

var supportedLangs = []string{"id", "en"}

// catalogs maps lang -> key -> message. Messages may contain fmt verbs.
var catalogs = mustLoadCatalogs()

func mustLoadCatalogs() map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, lang := range supportedLangs {
		b, err := localeFS.ReadFile("locales/" + lang + ".json")
		if err != nil {
			log.Fatalf("locale %s: %v", lang, err)
		}
		m := map[string]string{}
		if err := json.Unmarshal(b, &m); err != nil {
			log.Fatalf("locale %s: %v", lang, err)
		}
		out[lang] = m
	}
	return out
}

func isSupportedLang(lang string) bool {
	for _, l := range supportedLangs {
		if l == lang {
			return true
		}
	}
	return false
}

// requestLang is the cookie choice, else the default (Indonesian).
func requestLang(r *http.Request) string {
	if c, err := r.Cookie(langCookie); err == nil && isSupportedLang(c.Value) {
		return c.Value
	}
	return defaultLang
}

// translate looks key up in lang, then the default language, then returns the
// key itself so a missing string is visible rather than blank.
func translate(lang, key string, args ...any) string {
	msg, ok := catalogs[lang][key]
	if !ok {
		msg, ok = catalogs[defaultLang][key]
	}
	if !ok {
		return key
	}
	if len(args) > 0 {
		return fmt.Sprintf(msg, args...)
	}
	return msg
}

func (a *App) handleSetLang(w http.ResponseWriter, r *http.Request) {
	lang := strings.ToLower(r.PathValue("lang"))
	if !isSupportedLang(lang) {
		lang = defaultLang
	}
	http.SetCookie(w, &http.Cookie{
		Name:     langCookie,
		Value:    lang,
		Path:     "/",
		MaxAge:   365 * 24 * 3600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
	})
	http.Redirect(w, r, nextOrHome(r.URL.Query().Get("next")), http.StatusSeeOther)
}

// safeNext keeps only same-site paths, so ?next= can't redirect off-site.
// Browsers treat a backslash like a slash and drop tabs and newlines, so
// "/\\evil.com" or "/\t/evil.com" would read as "//evil.com"; url.Parse
// rejects every other control character.
func safeNext(raw string) string {
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.ContainsAny(raw, "\\\t\r\n") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return ""
	}
	return raw
}

func nextOrHome(raw string) string {
	if next := safeNext(raw); next != "" {
		return next
	}
	return "/"
}
