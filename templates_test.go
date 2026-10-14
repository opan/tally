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
