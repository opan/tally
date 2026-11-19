package main

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

// RFC-006: what a tally's admins know about each player (level, gender,
// member or guest), and the tally's own level scale. Only admin pages load
// these: ListParticipants and ParticipantByID leave Profile empty, so a
// public page can't show it by mistake.

const (
	maxLevels   = 10
	maxLevelLen = 10

	GenderMale   = "m"
	GenderFemale = "f"

	Member = "member"
	Guest  = "guest"
)

// defaultLevels is what "Pakai A+, A, B+, B, C" sets up, strongest first.
var defaultLevels = []string{"A+", "A", "B+", "B", "C"}

var (
	ErrBadProfile    = errors.New("unknown gender or status")
	ErrUnknownLevel  = errors.New("level not in the tally's list")
	ErrLevelExists   = errors.New("level exists")
	ErrTooManyLevels = errors.New("too many levels")
)

// Profile is a player's optional details; "" means not set.
type Profile struct {
	Level      string `json:",omitempty"`
	Gender     string `json:",omitempty"` // GenderMale, GenderFemale
	Membership string `json:",omitempty"` // Member, Guest
}

func validGender(g string) bool     { return g == "" || g == GenderMale || g == GenderFemale }
func validMembership(m string) bool { return m == "" || m == Member || m == Guest }

// cleanLevel trims and collapses spaces in a level name typed in Setelan.
func cleanLevel(s string) string { return strings.Join(strings.Fields(s), " ") }

func levelIndex(levels []string, name string) int {
	return slices.IndexFunc(levels, func(l string) bool { return strings.EqualFold(l, name) })
}

// ListRoster is ListParticipants with each player's Profile, for the Pemain
// page.
func (s *Store) ListRoster(ctx context.Context, tallyID string) ([]Participant, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, display_name, active, level, gender, membership FROM participants
		 WHERE tally_id = $1 ORDER BY active DESC, lower(display_name)`, tallyID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Participant, error) {
		var p Participant
		err := row.Scan(&p.ID, &p.Name, &p.Active, &p.Level, &p.Gender, &p.Membership)
		return p, err
	})
}

// TallyLevels is the tally's level scale, strongest first; nil when none is
// set up.
func (s *Store) TallyLevels(ctx context.Context, tallyID string) ([]string, error) {
	var levels []string
	err := s.pool.QueryRow(ctx, `SELECT levels FROM tallies WHERE id = $1`, tallyID).Scan(&levels)
	return levels, err
}

// LevelRow is a level in Setelan with how many players have it.
type LevelRow struct {
	Name    string
	Players int
}

func (s *Store) LevelRows(ctx context.Context, tallyID string) ([]LevelRow, error) {
	levels, err := s.TallyLevels(ctx, tallyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT level, count(*) FROM participants WHERE tally_id = $1 AND level <> '' GROUP BY level`, tallyID)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	var name string
	var n int
	if _, err := pgx.ForEachRow(rows, []any{&name, &n}, func() error { counts[name] = n; return nil }); err != nil {
		return nil, err
	}
	out := make([]LevelRow, len(levels))
	for i, l := range levels {
		out[i] = LevelRow{Name: l, Players: counts[l]}
	}
	return out, nil
}

// SetParticipantProfile saves a player's level, gender and status. The level
// must be one of the tally's (ErrUnknownLevel) or "".
func (s *Store) SetParticipantProfile(ctx context.Context, tallyID, actorID, id string, p Profile) error {
	if !validGender(p.Gender) || !validMembership(p.Membership) {
		return ErrBadProfile
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		levels, err := lockLevels(ctx, tx, tallyID)
		if err != nil {
			return err
		}
		if p.Level != "" && !slices.Contains(levels, p.Level) {
			return ErrUnknownLevel
		}
		before, err := s.participant(ctx, tx, tallyID, id)
		if err != nil {
			return err
		}
		if before.Profile == p {
			return nil
		}
		if _, err := tx.Exec(ctx,
			`UPDATE participants SET level = $3, gender = $4, membership = $5 WHERE tally_id = $1 AND id = $2`,
			tallyID, id, p.Level, p.Gender, p.Membership); err != nil {
			return err
		}
		after := *before
		after.Profile = p
		return audit(ctx, tx, tallyID, actorID, "update", "participant", id, before, after)
	})
}

// lockLevels reads the tally's levels and locks the tally row, so level edits
// and players being given a level don't race.
func lockLevels(ctx context.Context, tx pgx.Tx, tallyID string) ([]string, error) {
	var levels []string
	err := tx.QueryRow(ctx, `SELECT levels FROM tallies WHERE id = $1 FOR UPDATE`, tallyID).Scan(&levels)
	return levels, err
}

// levelsAudit is the audit snapshot of a level change: the list, and on the
// after side how many players were renamed or cleared with it.
type levelsAudit struct {
	Levels  []string `json:"levels"`
	Level   string   `json:"level,omitempty"`
	Players int      `json:"players,omitempty"`
}

// editLevels runs change on the tally's locked levels and saves the result.
// change returns the new list and, for a rename or removal, the level and its
// new name ("" clears it from players).
func (s *Store) editLevels(ctx context.Context, tallyID, actorID string,
	change func(levels []string) (next []string, from, to string, err error)) (players int, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		levels, err := lockLevels(ctx, tx, tallyID)
		if err != nil {
			return err
		}
		next, from, to, err := change(slices.Clone(levels))
		if err != nil {
			return err
		}
		if len(next) > maxLevels {
			return ErrTooManyLevels
		}
		if next == nil {
			next = []string{}
		}
		if _, err := tx.Exec(ctx, `UPDATE tallies SET levels = $2 WHERE id = $1`, tallyID, next); err != nil {
			return err
		}
		if from != "" {
			tag, err := tx.Exec(ctx, `UPDATE participants SET level = $3 WHERE tally_id = $1 AND level = $2`, tallyID, from, to)
			if err != nil {
				return err
			}
			players = int(tag.RowsAffected())
		}
		return audit(ctx, tx, tallyID, actorID, "update", "levels", tallyID,
			levelsAudit{Levels: levels}, levelsAudit{Levels: next, Level: from, Players: players})
	})
	return players, err
}

// AddLevel adds a level at the end of the list (the weakest).
func (s *Store) AddLevel(ctx context.Context, tallyID, actorID, name string) error {
	_, err := s.editLevels(ctx, tallyID, actorID, func(levels []string) ([]string, string, string, error) {
		if levelIndex(levels, name) >= 0 {
			return nil, "", "", ErrLevelExists
		}
		return append(levels, name), "", "", nil
	})
	return err
}

// UseDefaultLevels sets up A+, A, B+, B, C on a tally with no levels yet.
func (s *Store) UseDefaultLevels(ctx context.Context, tallyID, actorID string) error {
	_, err := s.editLevels(ctx, tallyID, actorID, func(levels []string) ([]string, string, string, error) {
		if len(levels) > 0 {
			return nil, "", "", ErrLevelExists
		}
		return slices.Clone(defaultLevels), "", "", nil
	})
	return err
}

// RenameLevel renames a level on the list and on every player who has it.
func (s *Store) RenameLevel(ctx context.Context, tallyID, actorID, old, name string) (int, error) {
	return s.editLevels(ctx, tallyID, actorID, func(levels []string) ([]string, string, string, error) {
		i := slices.Index(levels, old)
		if i < 0 {
			return nil, "", "", pgx.ErrNoRows
		}
		if j := levelIndex(levels, name); j >= 0 && j != i {
			return nil, "", "", ErrLevelExists
		}
		if levels[i] == name {
			return levels, "", "", nil
		}
		levels[i] = name
		return levels, old, name, nil
	})
}

// MoveLevel moves a level one place up (stronger) or down.
func (s *Store) MoveLevel(ctx context.Context, tallyID, actorID, name string, up bool) error {
	_, err := s.editLevels(ctx, tallyID, actorID, func(levels []string) ([]string, string, string, error) {
		i := slices.Index(levels, name)
		if i < 0 {
			return nil, "", "", pgx.ErrNoRows
		}
		j := i + 1
		if up {
			j = i - 1
		}
		if j >= 0 && j < len(levels) {
			levels[i], levels[j] = levels[j], levels[i]
		}
		return levels, "", "", nil
	})
	return err
}

// DeleteLevel removes a level and clears it from the players who had it;
// it returns how many.
func (s *Store) DeleteLevel(ctx context.Context, tallyID, actorID, name string) (int, error) {
	return s.editLevels(ctx, tallyID, actorID, func(levels []string) ([]string, string, string, error) {
		i := slices.Index(levels, name)
		if i < 0 {
			return nil, "", "", pgx.ErrNoRows
		}
		return slices.Delete(levels, i, i+1), name, "", nil
	})
}
