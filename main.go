package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "time/tzdata" // embed the tz database so Asia/Jakarta always resolves

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed templates
var tmplFS embed.FS

//go:embed static
var staticEmbed embed.FS

//go:embed schema.sql
var schemaSQL string

func main() {
	loadDotenv(".env") // local-dev convenience; Docker uses compose env_file

	dsn := env("DATABASE_URL", "postgres://tally:tally@localhost:5432/tally?sslmode=disable")
	port := env("PORT", "8080")

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("connect db: %v", err)
	}
	defer pool.Close()

	if err := waitForDB(ctx, pool); err != nil {
		log.Fatalf("database not ready: %v", err)
	}
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		log.Fatalf("apply schema: %v", err)
	}
	log.Print("database ready, schema applied")

	otelShutdown, otelOn := initOTel(ctx)
	if otelOn {
		registerBusinessMetrics(&Store{pool: pool})
		log.Print("otel metrics: enabled (OTLP export)")
	} else {
		log.Print("otel metrics: disabled (set OTEL_EXPORTER_OTLP_ENDPOINT to enable)")
	}

	aio, err := NewAioAuthFromEnv()
	if err != nil {
		log.Fatalf("login config: %v", err)
	}
	if aio.Enabled() {
		log.Printf("login: through all-in-one (%s)", aio.issuer)
	} else {
		log.Print("login: local username/password (set AUTH_PROVIDER=aio to log in through all-in-one)")
	}

	app := &App{store: &Store{pool: pool}, aio: aio, tmpl: buildTemplates(), guideVer: guideVersion(), guard: newSubmitGuard(24 * time.Hour), assetVer: assetVersion("static/style.css", "static/app.js", "static/poster.js", "static/vendor/html-to-image.js")}

	staticFS, err := fs.Sub(staticEmbed, "static")
	if err != nil {
		log.Fatalf("static fs: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", app.handleHome)
	mux.HandleFunc("GET /lang/{lang}", app.handleSetLang)
	mux.HandleFunc("GET /register", app.handleRegisterForm)
	mux.HandleFunc("POST /register", app.handleRegister)
	mux.HandleFunc("GET /login", app.handleLoginForm)
	mux.HandleFunc("POST /login", app.handleLogin)
	mux.HandleFunc("POST /logout", app.handleLogout)
	mux.HandleFunc("GET /auth/login", app.handleAuthLogin)
	mux.HandleFunc("GET /auth/callback", app.handleAuthCallback)
	// Read-only site admin pages; aio decides who is an admin on every request.
	mux.HandleFunc("GET /admin", app.handleAdminUsers)
	mux.HandleFunc("GET /admin/users/{id}", app.handleAdminUser)
	mux.HandleFunc("GET /panduan", app.handleGuide)
	mux.HandleFunc("GET /guide", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/panduan", http.StatusMovedPermanently)
	})
	mux.HandleFunc("POST /tallies", app.handleCreateTally)
	mux.HandleFunc("GET /t/{slug}", app.handleTally)
	mux.HandleFunc("GET /t/{slug}/players", app.handlePlayers)
	mux.HandleFunc("POST /t/{slug}/players", app.handleAddPlayers)
	mux.HandleFunc("POST /t/{slug}/players/{id}", app.handlePlayerAction)
	mux.HandleFunc("GET /t/{slug}/periods", app.handlePeriods)
	mux.HandleFunc("POST /t/{slug}/periods", app.handleCreatePeriod)
	mux.HandleFunc("GET /t/{slug}/periods/{id}", app.handleEditPeriodForm)
	mux.HandleFunc("POST /t/{slug}/periods/{id}", app.handleEditPeriod)
	mux.HandleFunc("GET /t/{slug}/play", app.handlePlay)
	mux.HandleFunc("POST /t/{slug}/rounds", app.handleCreateRound)
	mux.HandleFunc("GET /t/{slug}/rounds/{rid}", app.handleRound)
	mux.HandleFunc("POST /t/{slug}/rounds/{rid}/setup", app.handleRoundSetup)
	mux.HandleFunc("POST /t/{slug}/rounds/{rid}/shuffle", app.handleShuffle)
	mux.HandleFunc("POST /t/{slug}/rounds/{rid}/matches", app.handleAddMatch)
	mux.HandleFunc("POST /t/{slug}/rounds/{rid}/awards", app.handleAddAwards)
	mux.HandleFunc("POST /t/{slug}/awards/{aid}/delete", app.handleDeleteAward)
	mux.HandleFunc("GET /t/{slug}/players/{pid}", app.handlePlayer)
	mux.HandleFunc("GET /t/{slug}/matches/{mid}/edit", app.handleEditMatchForm)
	mux.HandleFunc("POST /t/{slug}/matches/{mid}", app.handleMatchAction)
	mux.HandleFunc("GET /t/{slug}/poster", app.handlePoster)
	mux.HandleFunc("GET /t/{slug}/logo", app.handleLogo)
	mux.HandleFunc("POST /t/{slug}/branding", app.handleSaveBranding)
	mux.HandleFunc("GET /t/{slug}/settings", app.handleSettings)
	mux.HandleFunc("POST /t/{slug}/settings", app.handleSaveSettings)
	mux.HandleFunc("POST /t/{slug}/admins", app.handleAddAdmin)
	mux.HandleFunc("POST /t/{slug}/admins/{uid}/remove", app.handleRemoveAdmin)
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServerFS(staticFS))))
	mux.HandleFunc("/", app.notFound)

	go pruneSessions(pool)

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           logRequests(securityHeaders(aio.origin(), app.withUser(otelMiddleware(mux)))),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()
	log.Printf("tally listening on http://localhost:%s", port)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Print("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	_ = otelShutdown(shutCtx)
}

// securityHeaders sets conservative, app-wide response headers. The CSP is
// strict (default-src 'self'); it works because all CSS/JS is same-origin and
// there are no inline scripts/handlers. The favicon is a data: URI (img-src).
// base-uri allows 'self' because the poster export sets a same-origin <base>.
// formActionExtra is an additional origin forms may submit to. Logout posts
// to /logout, which redirects to all-in-one's end_session; browsers apply
// form-action to that redirect too, so with AUTH_PROVIDER=aio the aio origin
// must be listed or logout silently stops at tally.
func securityHeaders(formActionExtra string, next http.Handler) http.Handler {
	formAction := "form-action 'self'"
	if formActionExtra != "" {
		formAction += " " + formActionExtra
	}
	csp := "default-src 'self'; img-src 'self' data:; base-uri 'self'; " + formAction + "; frame-ancestors 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// pruneSessions deletes expired sessions at startup and hourly thereafter.
func pruneSessions(pool *pgxpool.Pool) {
	for {
		if n, err := (&Store{pool: pool}).DeleteExpiredSessions(context.Background()); err != nil {
			log.Printf("session cleanup: %v", err)
		} else if n > 0 {
			log.Printf("session cleanup: removed %d expired", n)
		}
		time.Sleep(time.Hour)
	}
}

func waitForDB(ctx context.Context, pool *pgxpool.Pool) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := pool.Ping(ctx); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after 30s")
		}
		time.Sleep(time.Second)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// assetVersion returns a short content hash of the given embedded static files,
// appended as ?v=... to asset URLs so a deploy busts caches when bytes change.
func assetVersion(paths ...string) string {
	h := sha256.New()
	for _, p := range paths {
		b, err := staticEmbed.ReadFile(p)
		if err != nil {
			log.Printf("asset version: %s: %v", p, err)
			continue
		}
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:10]
}

// guideVersion hashes the user guide's screenshots, so regenerating them
// busts browser caches without touching the CSS/JS version.
func guideVersion() string {
	var paths []string
	_ = fs.WalkDir(staticEmbed, "static/guide", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			paths = append(paths, p)
		}
		return err
	})
	return assetVersion(paths...)
}

// cacheStatic marks versioned asset responses (requested with ?v=) as immutable.
func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		next.ServeHTTP(w, r)
	})
}

// loadDotenv reads simple KEY=VALUE lines from a .env file (if present) and sets
// any variables not already in the environment. Missing file is fine.
func loadDotenv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		log.Printf("%s %s -> %d (%s)", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
	})
}
