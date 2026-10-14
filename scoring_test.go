package main

import (
	"errors"
	"testing"
)

func TestScore(t *testing.T) {
	withBonus := func(c ScoringConfig, bonus int) ScoringConfig { c.WinBonus = bonus; return c }

	tests := []struct {
		name    string
		cfg     ScoringConfig
		a, b    []int
		want    [2]SideOutcome
		wantErr string
	}{
		{
			name: "continuous race to 42",
			cfg:  DefaultContinuous, a: []int{42}, b: []int{36},
			want: [2]SideOutcome{{42, ResultWin, 42}, {36, ResultLoss, 36}},
		},
		{
			name: "continuous with win bonus",
			cfg:  withBonus(DefaultContinuous, 5), a: []int{36}, b: []int{42},
			want: [2]SideOutcome{{36, ResultLoss, 36}, {42, ResultWin, 47}},
		},
		{
			name: "continuous deuce past target",
			cfg:  DefaultContinuous, a: []int{43}, b: []int{41},
			want: [2]SideOutcome{{43, ResultWin, 43}, {41, ResultLoss, 41}},
		},
		{
			name: "continuous unfinished match still counts",
			cfg:  DefaultContinuous, a: []int{38}, b: []int{35},
			want: [2]SideOutcome{{38, ResultWin, 38}, {35, ResultLoss, 35}},
		},
		{
			name: "continuous tie rejected",
			cfg:  DefaultContinuous, a: []int{30}, b: []int{30},
			wantErr: "score.err.tie",
		},
		{
			name: "reset 2-0 sums games",
			cfg:  DefaultReset, a: []int{21, 21}, b: []int{9, 19},
			want: [2]SideOutcome{{42, ResultWin, 42}, {28, ResultLoss, 28}},
		},
		{
			name: "reset 1-1 is a draw and gets no bonus",
			cfg:  withBonus(DefaultReset, 5), a: []int{21, 18}, b: []int{19, 21},
			want: [2]SideOutcome{{39, ResultDraw, 39}, {40, ResultDraw, 40}},
		},
		{
			name: "reset winner gets bonus",
			cfg:  withBonus(DefaultReset, 5), a: []int{22, 21}, b: []int{24, 30},
			want: [2]SideOutcome{{43, ResultLoss, 43}, {54, ResultWin, 59}},
		},
		{
			name: "reset tied game rejected",
			cfg:  DefaultReset, a: []int{21, 20}, b: []int{9, 20},
			wantErr: "score.err.game_tie",
		},
		{
			name: "outcome win uses win points, not the score",
			cfg:  DefaultOutcome, a: []int{3}, b: []int{1},
			want: [2]SideOutcome{{3, ResultWin, 3}, {1, ResultLoss, 0}},
		},
		{
			name: "outcome allows a draw",
			cfg:  DefaultOutcome, a: []int{2}, b: []int{2},
			want: [2]SideOutcome{{2, ResultDraw, 1}, {2, ResultDraw, 1}},
		},
		{
			name: "outcome with custom points",
			cfg:  ScoringConfig{Format: FormatOutcome, WinPts: 2, DrawPts: 1, LossPts: 1}, a: []int{0}, b: []int{1},
			want: [2]SideOutcome{{0, ResultLoss, 1}, {1, ResultWin, 2}},
		},
		{
			name: "counter seasons have no matches",
			cfg:  DefaultCounter, a: []int{1}, b: []int{0},
			wantErr: "score.err.format",
		},
		{
			name: "wrong number of games",
			cfg:  DefaultReset, a: []int{42}, b: []int{36},
			wantErr: "score.err.game_count",
		},
		{
			name: "negative score rejected",
			cfg:  DefaultContinuous, a: []int{-1}, b: []int{42},
			wantErr: "score.err.range",
		},
		{
			name: "typo-sized score rejected",
			cfg:  DefaultContinuous, a: []int{4200}, b: []int{36},
			wantErr: "score.err.range",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.cfg.Score(tt.a, tt.b)
			if tt.wantErr != "" {
				var se *ScoreError
				if !errors.As(err, &se) || se.Key != tt.wantErr {
					t.Fatalf("err = %v, want key %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseScoringConfig(t *testing.T) {
	c, err := ParseScoringConfig([]byte(`{"format":"continuous","target":42,"win_bonus":3}`))
	if err != nil || c.Target != 42 || c.WinBonus != 3 {
		t.Fatalf("got %+v, %v", c, err)
	}
	for _, raw := range []string{
		`{"format":"bogus"}`,
		`{"format":"continuous","target":0}`,
		`{"format":"reset","games":0,"game_target":21}`,
		`{"format":"continuous","target":42,"win_bonus":-1}`,
		`{"format":"outcome","win_pts":-3}`,
	} {
		if _, err := ParseScoringConfig([]byte(raw)); err == nil {
			t.Errorf("%s: expected error", raw)
		}
	}
}
