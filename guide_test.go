package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Both language versions of the guide must have the same sections (so deep
// links like /panduan#hari-main work in either language) and every screenshot
// they show must exist for that language.
func TestGuideSectionsAndScreenshots(t *testing.T) {
	sectionID := regexp.MustCompile(`<section class="card gsec" id="([a-z-]+)"`)
	shot := regexp.MustCompile(`"N" "([a-z-]+)"`)
	read := func(name string) string {
		b, err := fs.ReadFile(tmplFS, "templates/"+name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	ids := func(src string) []string {
		var out []string
		for _, m := range sectionID.FindAllStringSubmatch(src, -1) {
			out = append(out, m[1])
		}
		return out
	}
	toc := read("panduan.html")
	idSrc, enSrc := read("guide_id.html"), read("guide_en.html")
	if a, b := ids(idSrc), ids(enSrc); !slices.Equal(a, b) || len(a) == 0 {
		t.Fatalf("sections differ: id %v, en %v", a, b)
	}
	for _, id := range ids(idSrc) {
		if !strings.Contains(toc, `href="#`+id+`"`) {
			t.Errorf("section %q missing from the table of contents", id)
		}
	}
	for lang, src := range map[string]string{"id": idSrc, "en": enSrc} {
		var used []string
		for _, m := range shot.FindAllStringSubmatch(src, -1) {
			used = append(used, m[1])
			if _, err := fs.Stat(staticEmbed, "static/guide/"+lang+"/"+m[1]+".jpg"); err != nil {
				t.Errorf("%s guide shows %s.jpg, which doesn't exist", lang, m[1])
			}
		}
		entries, _ := fs.ReadDir(staticEmbed, "static/guide/"+lang)
		for _, e := range entries {
			if name := strings.TrimSuffix(e.Name(), ".jpg"); !slices.Contains(used, name) {
				t.Errorf("static/guide/%s/%s is never shown; remove it or use it", lang, e.Name())
			}
		}
	}
}

func TestGuidePageRenders(t *testing.T) {
	app := &App{tmpl: buildTemplates(), aio: &AioAuth{}}
	for _, lang := range []string{"id", "en"} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/panduan", nil)
		req.AddCookie(&http.Cookie{Name: langCookie, Value: lang})
		app.handleGuide(rr, req)
		body := rr.Body.String()
		if rr.Code != http.StatusOK || !strings.Contains(body, `id="hari-main"`) || !strings.Contains(body, "/static/guide/"+lang+"/courts.jpg") {
			t.Errorf("%s: status %d, guide content or screenshots missing", lang, rr.Code)
		}
	}
}
