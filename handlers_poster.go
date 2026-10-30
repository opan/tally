package main

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg" // register decoders for logo validation
	_ "image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	_ "golang.org/x/image/webp"
)

const (
	maxLogoBytes    = 1 << 20 // 1 MiB
	maxLogoSide     = 4096
	maxBrandLineLen = 40
	maxFooterLen    = 60
	maxPosterRows   = 20
)

var logoTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true}

// posterVM is the leaderboard plus the poster's resolved text.
type posterVM struct {
	tallyVM
	Title1   string
	Title2   string
	Badge    string
	Footer   string
	Rows     []BoardRow
	Density  string // "", "dense", "denser": shrinks rows so long boards fit
	Filename string
}

// splitTitle breaks a tally name into a white and a gold line at the word
// boundary closest to the middle: "Bala Bulu Point Rank" -> "Bala Bulu" / "Point Rank".
func splitTitle(name string) (string, string) {
	words := strings.Fields(name)
	if len(words) < 2 {
		return name, ""
	}
	best, bestDiff := 1, 1<<30
	for i := 1; i < len(words); i++ {
		l, r := len(strings.Join(words[:i], " ")), len(strings.Join(words[i:], " "))
		if d := max(l-r, r-l); d < bestDiff {
			best, bestDiff = i, d
		}
	}
	return strings.Join(words[:best], " "), strings.Join(words[best:], " ")
}

func posterDensity(rows int) string {
	switch {
	case rows > 14:
		return "denser"
	case rows > 10:
		return "dense"
	}
	return ""
}

func (a *App) handlePoster(w http.ResponseWriter, r *http.Request) {
	tc, ok := a.loadTally(w, r, "overview")
	if !ok {
		return
	}
	board, ok := a.loadBoard(w, r, tc)
	if !ok {
		return
	}
	b := tc.Tally.Branding
	vm := posterVM{tallyVM: board, Title1: b.Title1, Title2: b.Title2, Badge: b.Badge, Footer: b.Footer}
	if vm.Title1 == "" && vm.Title2 == "" {
		vm.Title1, vm.Title2 = splitTitle(tc.Tally.Name)
	}
	vm.Rows = board.Board
	if len(vm.Rows) > maxPosterRows {
		vm.Rows = vm.Rows[:maxPosterRows]
	}
	vm.Density = posterDensity(len(vm.Rows))
	vm.Filename = tc.Tally.Slug
	if board.Day != nil {
		vm.Filename += "-minggu-" + strings.ToLower(board.Day.WeekLabel()) + "-" + board.Day.PlayedOn.Format(dateLayout)
	} else if board.Round != nil {
		vm.Filename += "-" + board.Round.PlayedOn.Format(dateLayout)
	}
	vm.Filename += ".png"
	a.render(w, r, "poster", vm)
}

func (a *App) handleLogo(w http.ResponseWriter, r *http.Request) {
	tc, ok := a.loadTally(w, r, "")
	if !ok {
		return
	}
	ct, data, err := a.store.Logo(r.Context(), tc.Tally.ID)
	if isNotFound(err) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		internalError(w, "load logo", err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	if r.URL.Query().Get("v") != "" {
		cache := "public"
		if tc.Tally.Visibility == VisibilityPrivate {
			cache = "private"
		}
		h.Set("Cache-Control", cache+", max-age=31536000, immutable")
	}
	_, _ = w.Write(data)
}

// readLogo validates an uploaded logo: PNG, JPEG, or WebP that really decodes,
// at most 1 MiB and 4096px a side. SVG is refused (it can carry script).
func readLogo(r *http.Request) (contentType string, data []byte, errKey string) {
	f, _, err := r.FormFile("logo")
	if errors.Is(err, http.ErrMissingFile) {
		return "", nil, ""
	}
	if err != nil {
		return "", nil, "err.logo_invalid"
	}
	defer f.Close()
	data, err = io.ReadAll(io.LimitReader(f, maxLogoBytes+1))
	if err != nil {
		return "", nil, "err.logo_invalid"
	}
	if len(data) > maxLogoBytes {
		return "", nil, "err.logo_too_big"
	}
	contentType = http.DetectContentType(data)
	if !logoTypes[contentType] {
		return "", nil, "err.logo_type"
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", nil, "err.logo_invalid"
	}
	if cfg.Width > maxLogoSide || cfg.Height > maxLogoSide {
		return "", nil, "err.logo_too_big"
	}
	return contentType, data, ""
}

func (a *App) handleSaveBranding(w http.ResponseWriter, r *http.Request) {
	tc, u, ok := a.adminTally(w, r, "settings")
	if !ok {
		return
	}
	limitBody(w, r, maxLogoBytes+maxFormBytes)
	if err := r.ParseMultipartForm(maxLogoBytes + maxFormBytes); err != nil {
		tc.Err = tr(r, "err.logo_too_big")
		a.renderSettings(w, r, settingsVM{tallyCtx: tc}, http.StatusUnprocessableEntity)
		return
	}
	clean := func(name string) string { return strings.Join(strings.Fields(r.FormValue(name)), " ") }
	b := Branding{Title1: clean("title1"), Title2: clean("title2"), Badge: clean("badge"), Footer: clean("footer")}
	fail := func(key string, args ...any) {
		tc.Err = tr(r, key, args...)
		failed := *tc.Tally
		failed.Branding = b // re-render with what was typed
		tc.Tally = &failed
		a.renderSettings(w, r, settingsVM{tallyCtx: tc}, http.StatusUnprocessableEntity)
	}
	for _, line := range []string{b.Title1, b.Title2, b.Badge} {
		if tooLong(line, maxBrandLineLen) {
			fail("err.brand_long", maxBrandLineLen)
			return
		}
	}
	if tooLong(b.Footer, maxFooterLen) {
		fail("err.brand_long", maxFooterLen)
		return
	}
	ct, data, errKey := readLogo(r)
	if errKey != "" {
		fail(errKey)
		return
	}

	ctx := r.Context()
	if err := a.store.UpdateBranding(ctx, u.ID, tc.Tally, b); err != nil {
		internalError(w, "update branding", err)
		return
	}
	switch {
	case data != nil:
		err := a.store.SetLogo(ctx, u.ID, tc.Tally.ID, ct, data)
		if err != nil {
			internalError(w, "set logo", err)
			return
		}
	case r.FormValue("remove_logo") == "1":
		if err := a.store.DeleteLogo(ctx, u.ID, tc.Tally.ID); err != nil {
			internalError(w, "delete logo", err)
			return
		}
	}
	http.Redirect(w, r, tallyPath(tc.Tally, "/settings")+"?ok=branding_saved#poster", http.StatusSeeOther)
}

// MonthRange renders a season as "OKT – DES 2026", or "NOV 2026 – JAN 2027"
// across a year boundary.
func (p pageData) MonthRange(from, to time.Time) string {
	months := strings.Split(p.T("month.short"), ",")
	m := func(t time.Time) string {
		if len(months) == 12 {
			return months[t.Month()-1]
		}
		return t.Format("Jan")
	}
	if from.Year() == to.Year() {
		if from.Month() == to.Month() {
			return m(from) + " " + strconv.Itoa(from.Year())
		}
		return m(from) + " – " + m(to) + " " + strconv.Itoa(to.Year())
	}
	return m(from) + " " + strconv.Itoa(from.Year()) + " – " + m(to) + " " + strconv.Itoa(to.Year())
}
