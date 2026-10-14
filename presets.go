package main

const (
	KindMatch   = "match"   // two sides play; points come from the score
	KindCounter = "counter" // no matches; admins enter points directly
)

// Preset is a starting point for a tally: what kind of activity it records,
// players per side, and the default points rule for new seasons. The tally
// stores only the preset ID (plus a side size for team presets).
type Preset struct {
	ID        string
	Kind      string
	SideSize  int
	TeamSizes []int // non-empty: the side size is chosen when creating the tally
	Scoring   ScoringConfig
	Formats   []string // score formats a season may use
	Sport     string   // drives the poster emblem
}

var (
	rallyFormats   = []string{FormatContinuous, FormatReset}
	genericFormats = []string{FormatOutcome, FormatContinuous, FormatReset}
)

var presets = []Preset{
	{ID: "badminton_doubles", Kind: KindMatch, SideSize: 2, Scoring: DefaultContinuous, Formats: rallyFormats, Sport: "badminton"},
	{ID: "badminton_singles", Kind: KindMatch, SideSize: 1, Scoring: DefaultContinuous, Formats: rallyFormats, Sport: "badminton"},
	{ID: "duel", Kind: KindMatch, SideSize: 1, Scoring: DefaultOutcome, Formats: genericFormats},
	{ID: "pairs", Kind: KindMatch, SideSize: 2, Scoring: DefaultOutcome, Formats: genericFormats},
	{ID: "team", Kind: KindMatch, SideSize: 5, TeamSizes: []int{3, 4, 5, 6, 7, 8, 9, 10, 11}, Scoring: DefaultOutcome, Formats: genericFormats},
	{ID: "counter", Kind: KindCounter, Scoring: DefaultCounter, Formats: []string{FormatCounter}},
}

func presetByID(id string) (Preset, bool) {
	for _, p := range presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

// NameKey and HelpKey are the i18n keys for the preset's name and description.
func (p Preset) NameKey() string { return "preset." + p.ID }
func (p Preset) HelpKey() string { return "preset." + p.ID + ".help" }

func (p Preset) IsCounter() bool { return p.Kind == KindCounter }

func (p Preset) AllowsFormat(f string) bool {
	for _, x := range p.Formats {
		if x == f {
			return true
		}
	}
	return false
}

func (p Preset) AllowsTeamSize(n int) bool {
	for _, x := range p.TeamSizes {
		if x == n {
			return true
		}
	}
	return false
}
