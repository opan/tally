package main

import (
	"encoding/json"
	"fmt"
)

const (
	FormatContinuous = "continuous" // one running score, race to Target
	FormatReset      = "reset"      // Games games, each to GameTarget, score resets per game
	FormatOutcome    = "outcome"    // one score; points by result (WinPts/DrawPts/LossPts)
	FormatCounter    = "counter"    // no matches; points are entered directly

	maxScore = 999 // sanity bound against typos (e.g. 4200 instead of 42)
)

type Result string

const (
	ResultWin  Result = "win"
	ResultDraw Result = "draw"
	ResultLoss Result = "loss"
)

// ScoringConfig is a period's points rule, stored as JSON on the period.
type ScoringConfig struct {
	Format     string `json:"format"`
	Target     int    `json:"target,omitempty"`      // continuous
	Games      int    `json:"games,omitempty"`       // reset
	GameTarget int    `json:"game_target,omitempty"` // reset
	WinBonus   int    `json:"win_bonus"`             // continuous, reset
	WinPts     int    `json:"win_pts,omitempty"`     // outcome
	DrawPts    int    `json:"draw_pts,omitempty"`    // outcome
	LossPts    int    `json:"loss_pts,omitempty"`    // outcome
}

var (
	DefaultContinuous = ScoringConfig{Format: FormatContinuous, Target: 42}
	DefaultReset      = ScoringConfig{Format: FormatReset, Games: 2, GameTarget: 21}
	DefaultOutcome    = ScoringConfig{Format: FormatOutcome, WinPts: 3, DrawPts: 1}
	DefaultCounter    = ScoringConfig{Format: FormatCounter}
)

// ScoreError carries an i18n key so handlers can show a localized message.
type ScoreError struct {
	Key  string
	Args []any
}

func (e *ScoreError) Error() string { return fmt.Sprint(append([]any{e.Key}, e.Args...)...) }

func scoreErr(key string, args ...any) error { return &ScoreError{Key: key, Args: args} }

func ParseScoringConfig(raw []byte) (ScoringConfig, error) {
	var c ScoringConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	return c, c.Validate()
}

func (c ScoringConfig) Validate() error {
	if c.WinBonus < 0 || c.WinBonus > maxScore {
		return scoreErr("score.err.bonus")
	}
	switch c.Format {
	case FormatContinuous:
		if c.Target < 1 || c.Target > maxScore {
			return scoreErr("score.err.target")
		}
	case FormatReset:
		if c.Games < 1 || c.Games > 5 {
			return scoreErr("score.err.games")
		}
		if c.GameTarget < 1 || c.GameTarget > maxScore {
			return scoreErr("score.err.target")
		}
	case FormatOutcome:
		for _, v := range []int{c.WinPts, c.DrawPts, c.LossPts} {
			if v < 0 || v > maxScore {
				return scoreErr("score.err.outcome_pts")
			}
		}
	case FormatCounter:
	default:
		return scoreErr("score.err.format")
	}
	return nil
}

// GamesPerMatch is how many score boxes each side fills in.
func (c ScoringConfig) GamesPerMatch() int {
	if c.Format == FormatReset {
		return c.Games
	}
	return 1
}

// AllowsDraw reports whether an equal score is a valid result.
func (c ScoringConfig) AllowsDraw() bool { return c.Format == FormatOutcome }

// SideOutcome is one side's computed result of a match.
type SideOutcome struct {
	Total  int    // sum of the side's games
	Result Result // win | draw | loss
	Points int    // what each player on this side earns
}

func (c ScoringConfig) pointsFor(o SideOutcome) int {
	if c.Format == FormatOutcome {
		switch o.Result {
		case ResultWin:
			return c.WinPts
		case ResultDraw:
			return c.DrawPts
		}
		return c.LossPts
	}
	if o.Result == ResultWin {
		return o.Total + c.WinBonus
	}
	return o.Total
}

// Score turns two sides' game scores into totals, results, and points.
// Validation is loose on purpose: deuce scores and unfinished matches (court
// time ran out) are accepted; only clearly invalid input is rejected.
func (c ScoringConfig) Score(a, b []int) ([2]SideOutcome, error) {
	var out [2]SideOutcome
	if c.Format == FormatCounter {
		return out, scoreErr("score.err.format")
	}
	n := c.GamesPerMatch()
	if len(a) != n || len(b) != n {
		return out, scoreErr("score.err.game_count", n)
	}
	var winsA, winsB int
	for i := range n {
		if a[i] < 0 || b[i] < 0 || a[i] > maxScore || b[i] > maxScore {
			return out, scoreErr("score.err.range")
		}
		out[0].Total += a[i]
		out[1].Total += b[i]
		if a[i] == b[i] && c.AllowsDraw() {
			continue
		}
		if a[i] == b[i] {
			if n == 1 {
				return out, scoreErr("score.err.tie")
			}
			return out, scoreErr("score.err.game_tie", i+1)
		}
		if a[i] > b[i] {
			winsA++
		} else {
			winsB++
		}
	}

	switch {
	case winsA > winsB:
		out[0].Result, out[1].Result = ResultWin, ResultLoss
	case winsB > winsA:
		out[0].Result, out[1].Result = ResultLoss, ResultWin
	default:
		out[0].Result, out[1].Result = ResultDraw, ResultDraw
	}
	for i := range out {
		out[i].Points = c.pointsFor(out[i])
	}
	return out, nil
}
