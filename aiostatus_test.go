package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUserStatus(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		want    aioUserStatus
		wantErr bool
	}{
		{"admin", 200, `{"success":true,"data":{"admin":true,"blocked":false}}`, aioUserStatus{Admin: true}, false},
		{"not admin", 200, `{"success":true,"data":{"admin":false,"blocked":false}}`, aioUserStatus{}, false},
		{"blocked is never admin", 200, `{"success":true,"data":{"admin":true,"blocked":true}}`, aioUserStatus{Blocked: true}, false},
		{"unknown user", 404, `{"success":false,"error":"user not found"}`, aioUserStatus{}, false},
		{"wrong credentials", 401, `{"success":false}`, aioUserStatus{}, true},
		{"aio error", 500, `{"success":false}`, aioUserStatus{}, true},
		{"unsuccessful body", 200, `{"success":false}`, aioUserStatus{}, true},
		{"garbage body", 200, `not json`, aioUserStatus{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var gotPath, gotUser, gotPass string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.EscapedPath()
				gotUser, gotPass, _ = r.BasicAuth()
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()

			a := &AioAuth{enabled: true, clientID: "tally", clientSecret: "s3cret",
				internalURL: srv.URL, httpClient: &http.Client{Timeout: time.Second}}
			got, err := a.userStatus(context.Background(), "6f1c2a0e-8f4b-4f51-9a39-0d6f3d1e2b7a")
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if got != c.want {
				t.Errorf("status = %+v, want %+v", got, c.want)
			}
			if gotPath != "/api/v1/oidc/users/6f1c2a0e-8f4b-4f51-9a39-0d6f3d1e2b7a/status" {
				t.Errorf("path = %q", gotPath)
			}
			if gotUser != "tally" || gotPass != "s3cret" {
				t.Errorf("basic auth = %q/%q", gotUser, gotPass)
			}
		})
	}
}

func TestUserStatus_EscapesSubAndTimesOut(t *testing.T) {
	block := make(chan struct{})
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		<-block
	}))
	defer srv.Close()
	defer close(block)

	a := &AioAuth{enabled: true, internalURL: srv.URL, httpClient: &http.Client{Timeout: 50 * time.Millisecond}}
	if _, err := a.userStatus(context.Background(), "../../clients"); err == nil {
		t.Fatal("expected a timeout error")
	}
	if !strings.HasPrefix(gotPath, "/api/v1/oidc/users/..%2F..%2Fclients/status") {
		t.Errorf("sub not escaped: %q", gotPath)
	}
}

func TestInternalURLFallsBackToIssuer(t *testing.T) {
	base := map[string]string{"AUTH_PROVIDER": "aio", "AIO_ISSUER": "https://aio.example.com/",
		"AIO_CLIENT_ID": "tally", "AIO_CLIENT_SECRET": "x", "AIO_REDIRECT_URL": "https://tally.example.com/auth/callback"}
	for _, c := range []struct{ internal, want string }{
		{"", "https://aio.example.com"},
		{"http://all-in-one.app.svc.cluster.local/", "http://all-in-one.app.svc.cluster.local"},
	} {
		for k, v := range base {
			t.Setenv(k, v)
		}
		t.Setenv("AIO_INTERNAL_URL", c.internal)
		a, err := NewAioAuthFromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if a.internalURL != c.want {
			t.Errorf("AIO_INTERNAL_URL=%q: internalURL = %q, want %q", c.internal, a.internalURL, c.want)
		}
	}
}

func TestAdminTemplatesRender(t *testing.T) {
	tmpl := buildTemplates()
	last := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	joined := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	users := adminUsersVM{Users: []AdminUserRow{
		{User: User{ID: "u1", Username: "budi", CreatedAt: joined, AioUserID: "a1"}, TallyCount: 2, LastActivity: &last},
		{User: User{ID: "u2", Username: "lama", CreatedAt: joined}},
	}}
	user := adminUserVM{User: &User{ID: "u1", Username: "budi", CreatedAt: joined, AioUserID: "a1"},
		Tallies: []AdminTallyRow{
			{Tally: Tally{Slug: "bala-bulu", Name: "Bala Bulu", Visibility: "link"}, Role: "owner", Players: 12, PlayDays: 3, Matches: 20, LastPlayed: &last},
			{Tally: Tally{Slug: "rahasia", Name: "Rahasia", Visibility: "private"}, Role: "admin"},
		}}
	cases := []struct {
		page, lang string
		data       any
		want       []string
		notWant    []string
	}{
		{"admin", "en", users, []string{"Site admin", "budi", "/admin/users/u1", "local account"}, nil},
		{"admin", "id", users, []string{"Admin situs", "akun lokal"}, nil},
		{"adminuser", "en", user, []string{`href="/t/bala-bulu"`, "Bala Bulu", "Rahasia", "owner"}, []string{`href="/t/rahasia"`}},
		{"adminerror", "id", nil, []string{"all-in-one sedang tidak terjangkau"}, nil},
	}
	for _, c := range cases {
		t.Run(c.page+"/"+c.lang, func(t *testing.T) {
			var buf bytes.Buffer
			pd := pageData{Username: "admin", IsAdmin: true, Lang: c.lang, Path: "/admin", Data: c.data}
			if err := tmpl[c.page].ExecuteTemplate(&buf, "layout", pd); err != nil {
				t.Fatal(err)
			}
			out := buf.String()
			for _, s := range append(c.want, `href="/admin"`) {
				if !strings.Contains(out, s) {
					t.Errorf("missing %q", s)
				}
			}
			for _, s := range c.notWant {
				if strings.Contains(out, s) {
					t.Errorf("must not contain %q", s)
				}
			}
		})
	}
}
