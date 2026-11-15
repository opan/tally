package main

import (
	"strings"
	"testing"
)

func TestTemplatesParse(t *testing.T) {
	pages := buildTemplates() // panics on a parse error
	for _, name := range pageNames {
		if pages[name] == nil {
			t.Errorf("page %q missing", name)
		}
	}
}

// The play day's Poin/Pertandingan switch must not hide tallyCtx.Tab, which
// highlights "Main" in the tally menu.
func TestRoundVMKeepsMenuTab(t *testing.T) {
	vm := roundVM{tallyCtx: tallyCtx{Tab: "play"}, DayTab: tabPoints}
	if vm.Tab != "play" {
		t.Fatalf("roundVM.Tab = %q, want the menu tab %q", vm.Tab, "play")
	}
}

// The tally menu (RFC-005 step 3) is for admins only. Every item is in the
// sidebar; the phone's bottom bar has the Bar ones, and Lainnya the rest, lit
// when the current page is one of those.
func TestTallyMenu(t *testing.T) {
	tmpl := buildTemplates()
	render := func(tc tallyCtx) string {
		var buf strings.Builder
		if err := tmpl["notfound"].ExecuteTemplate(&buf, "layout", pageData{Lang: "id", Data: tc}); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	tl := &Tally{Slug: "sampai-pingsan"}

	if out := render(tallyCtx{Tally: tl, Tab: "overview"}); strings.Contains(out, "menubar") || strings.Contains(out, "sidemenu") {
		t.Error("a member sees the tally menu")
	}

	out := render(tallyCtx{Tally: tl, Role: RoleAdmin, Tab: "players"})
	for _, s := range []string{`class="sidemenu"`, `class="menubar"`, `href="/t/sampai-pingsan/settings"`, `popovertarget="menumore"`, "Lainnya"} {
		if !strings.Contains(out, s) {
			t.Errorf("admin page: missing %q", s)
		}
	}
	if n := strings.Count(out, `class="on" aria-current="page"`); n != 2 { // sidebar and bar
		t.Errorf("Pemain lit %d times, want 2", n)
	}
	if strings.Contains(out, `popovertarget="menumore" class="on"`) {
		t.Error("Lainnya lit on a bar page")
	}

	out = render(tallyCtx{Tally: tl, Role: RoleOwner, Tab: "settings"})
	if !strings.Contains(out, `popovertarget="menumore" class="on"`) {
		t.Error("Lainnya not lit on Setelan")
	}
	if strings.Contains(out, "/iuran") {
		t.Error("Iuran in the menu before any fee")
	}
	if !strings.Contains(out, `href="/panduan?t=sampai-pingsan"`) {
		t.Error("Panduan doesn't keep the tally menu")
	}

	// With fees, Iuran joins the menu: in the sidebar, and on a phone in
	// Lainnya (lit there), with Musim staying on the bar.
	out = render(tallyCtx{Tally: tl, Role: RoleAdmin, Tab: "iuran", Fees: true})
	side := out[strings.Index(out, `class="sidemenu"`):strings.Index(out, `class="menubar"`)]
	bar := out[strings.Index(out, `class="menubar"`):strings.Index(out, `id="menumore"`)]
	sheet := out[strings.Index(out, `id="menumore"`):]
	switch {
	case !strings.Contains(side, `href="/t/sampai-pingsan/iuran" class="on"`):
		t.Error("Iuran not lit in the sidebar")
	case strings.Contains(bar, "/iuran") || !strings.Contains(bar, "/periods"):
		t.Error("phone bar: want Musim, not Iuran")
	case !strings.Contains(sheet, `href="/t/sampai-pingsan/iuran" class="on"`) || !strings.Contains(bar, `popovertarget="menumore" class="on"`):
		t.Error("Iuran not lit in Lainnya")
	}

	// The guide opened from the menu: Panduan lit, in the sidebar and in Lainnya.
	var buf strings.Builder
	vm := guideVM{tallyCtx: tallyCtx{Tally: tl, Role: RoleAdmin, Tab: "guide"}}
	if err := tmpl["panduan"].ExecuteTemplate(&buf, "layout", pageData{Lang: "id", Data: vm}); err != nil {
		t.Fatal(err)
	}
	out = buf.String()
	for _, s := range []string{`class="sidemenu-guide on" aria-current="page"`, `popovertarget="menumore" class="on"`, `href="/t/sampai-pingsan/players"`} {
		if !strings.Contains(out, s) {
			t.Errorf("guide with a tally: missing %q", s)
		}
	}
}
