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
}
