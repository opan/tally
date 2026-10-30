package main

import "testing"

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
