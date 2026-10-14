package main

import (
	"regexp"
	"strings"
)

var slugRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// reservedSlugs can't be used as tally links, so /t/{slug} never collides with
// a future top-level feature.
var reservedSlugs = map[string]bool{"new": true, "admin": true, "api": true, "static": true}

// normalizeSlug lowercases and converts spaces/underscores to single hyphens,
// stripping anything that isn't [a-z0-9-]. "Bala Bulu BC!" -> "bala-bulu-bc".
func normalizeSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastHyphen := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastHyphen = false
		case r == ' ' || r == '-' || r == '_':
			if !lastHyphen && b.Len() > 0 {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// validateSlug returns an i18n key, or "" when valid.
func validateSlug(s string) string {
	switch {
	case len(s) < 3:
		return "err.slug_short"
	case len(s) > 40:
		return "err.slug_long"
	case !slugRe.MatchString(s):
		return "err.slug_chars"
	case reservedSlugs[s]:
		return "err.slug_taken"
	}
	return ""
}
