package main

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type ctxKey int

const userCtxKey ctxKey = iota

const (
	sessionCookie  = "tally_session"
	sessionTTL     = 30 * 24 * time.Hour
	minPasswordLen = 8
	maxPasswordLen = 72 // bcrypt hashes at most 72 bytes
)

func hashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

func checkPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

var usernameRe = regexp.MustCompile(`^[a-z0-9_]+$`)

// normalizeUsername lowercases and trims, so "Budi" and "budi" are the same account.
func normalizeUsername(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// validateUsername returns an i18n key, or "" when valid.
func validateUsername(s string) string {
	switch {
	case len(s) < 3:
		return "err.username_short"
	case len(s) > 30:
		return "err.username_long"
	case !usernameRe.MatchString(s):
		return "err.username_chars"
	}
	return ""
}

// validatePassword returns an i18n key, or "" when valid.
func validatePassword(pw, confirm string) string {
	switch {
	case len(pw) < minPasswordLen:
		return "err.password_short"
	case len(pw) > maxPasswordLen:
		return "err.password_long"
	case pw != confirm:
		return "err.password_mismatch"
	}
	return ""
}

// withUser loads the current user (if any) from the session cookie into context.
func (a *App) withUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			if u, err := a.store.UserBySession(r.Context(), c.Value); err == nil {
				r = r.WithContext(context.WithValue(r.Context(), userCtxKey, u))
			}
		}
		next.ServeHTTP(w, r)
	})
}

func currentUser(r *http.Request) *User {
	u, _ := r.Context().Value(userCtxKey).(*User)
	return u
}

// startSession logs a user in. idToken is reserved for logins through aio.
func (a *App) startSession(w http.ResponseWriter, r *http.Request, userID, idToken string) error {
	token := newToken() + newToken() // 32 random bytes of entropy
	expires := time.Now().Add(sessionTTL)
	if err := a.store.CreateSession(r.Context(), token, userID, expires, idToken); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
	})
	return nil
}

func (a *App) endSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = a.store.DeleteSession(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}
