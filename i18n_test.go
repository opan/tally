package main

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestLocalesHaveSameKeys(t *testing.T) {
	base := catalogs[defaultLang]
	for _, lang := range supportedLangs {
		for key := range base {
			if _, ok := catalogs[lang][key]; !ok {
				t.Errorf("%s: missing key %q", lang, key)
			}
		}
		for key := range catalogs[lang] {
			if _, ok := base[key]; !ok {
				t.Errorf("%s: key %q not in %s", lang, key, defaultLang)
			}
		}
	}
}

// Every key referenced from templates (.T "key") or Go code ("err.*",
// "score.err.*", "auth.err.*") must exist, so a typo can't ship as a raw key on screen.
func TestReferencedKeysExist(t *testing.T) {
	tmplKey := regexp.MustCompile(`\.T "([^"]+)"`)
	goKeys := []*regexp.Regexp{
		regexp.MustCompile(`"((?:score\.|auth\.)?err\.[a-z_]+)"`),
		regexp.MustCompile(`(?:tr\(r|p\.T\(|translate\(lang), "([^"]+)"`),
	}
	used := map[string]bool{}

	_ = fs.WalkDir(tmplFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := fs.ReadFile(tmplFS, path)
		for _, m := range tmplKey.FindAllStringSubmatch(string(b), -1) {
			used[m[1]] = true
		}
		return nil
	})
	for _, src := range []string{handlersSrc, handlersTallySrc, handlersMatchSrc, handlersPosterSrc, handlersPlayerSrc, authSrc, scoringSrc, aioauthSrc, slugSrc, datesSrc} {
		for _, re := range goKeys {
			for _, m := range re.FindAllStringSubmatch(src, -1) {
				if !strings.HasSuffix(m[1], ".") { // a prefix of a dynamic key, checked below
					used[m[1]] = true
				}
			}
		}
	}
	for k := range okNotices {
		used["ok."+k] = true
	}
	for _, p := range presets {
		used[p.NameKey()] = true
		used[p.HelpKey()] = true
		for _, f := range p.Formats {
			used["format."+f] = true
			used["format."+f+"_help"] = true
		}
	}

	var missing []string
	for key := range used {
		if _, ok := catalogs[defaultLang][key]; !ok {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("keys used but not defined: %s", strings.Join(missing, ", "))
	}
	if len(used) < 10 {
		t.Fatalf("found only %d keys; extraction regex broken?", len(used))
	}
}

func TestTranslate(t *testing.T) {
	if got := translate("en", "score.err.game_tie", 2); got != "Game 2 can't be a tie." {
		t.Errorf("en with args: %q", got)
	}
	if got := translate("xx", "login.title"); got != "Masuk" {
		t.Errorf("unknown lang should fall back to id: %q", got)
	}
	if got := translate("en", "no.such.key"); got != "no.such.key" {
		t.Errorf("missing key should echo the key: %q", got)
	}
}

func TestSafeNext(t *testing.T) {
	for raw, want := range map[string]string{
		"/t/bala-bulu?x=1": "/t/bala-bulu?x=1",
		"//evil.com":       "",
		"/\\evil.com":      "",
		"https://evil.com": "",
		"":                 "",
		// Browsers drop tabs/newlines and read "\\" as "/", so these are "//evil.com".
		"/\t/evil.com": "",
		"/\n/evil.com": "",
		"/a\\b":        "",
		"/\x7f":        "",
	} {
		if got := safeNext(raw); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestSplitNames(t *testing.T) {
	got := splitNames("1. Dhamar\n2) Dimas\n- QQ\n  ferdian  \n\nFerdian\n• AD, Ilham\nGatot   Subroto\n")
	want := []string{"Dhamar", "Dimas", "QQ", "ferdian", "AD", "Ilham", "Gatot Subroto"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatDate(t *testing.T) {
	d, _ := parseDate("2026-10-11")
	if got := formatDate("id", d); got != "11 Okt 2026" {
		t.Errorf("id: %q", got)
	}
	if got := formatDate("en", d); got != "11 Oct 2026" {
		t.Errorf("en: %q", got)
	}
}
