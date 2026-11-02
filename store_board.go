package main

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Leaderboard (papan) is one sport or game inside a tally (RFC-003): its type
// decides counter or matches, players per side and the season formats, and
// its seasons, play days and board are its own. Players, admins, visibility
// and branding stay on the tally.
type Leaderboard struct {
	ID          string
	TallyID     string
	Slug        string
	Name        string // "" = the type's translated name
	Template    string
	SideSizeSet *int // chosen players per side (team presets); nil = preset's
	Position    int
	CreatedAt   time.Time
}

func (b *Leaderboard) Preset() Preset {
	if p, ok := presetByID(b.Template); ok {
		return p
	}
	return presets[0]
}

// SideSize is how many players play on each side.
func (b *Leaderboard) SideSize() int {
	if b.SideSizeSet != nil {
		return *b.SideSizeSet
	}
	return b.Preset().SideSize
}

// boardSlug is a new leaderboard's slug from its type; the upgrade in
// schema.sql does the same in SQL.
func boardSlug(p Preset) string {
	if p.Sport != "" {
		return p.Sport
	}
	return strings.ReplaceAll(p.ID, "_", "-")
}

const boardCols = `b.id, b.tally_id, b.slug, b.name, b.template, b.side_size, b.position, b.created_at`

// boardOrder puts the default leaderboard first.
const boardOrder = ` ORDER BY b.position, b.created_at, b.id`

func scanBoard(row pgx.Row) (*Leaderboard, error) {
	b := &Leaderboard{}
	if err := row.Scan(&b.ID, &b.TallyID, &b.Slug, &b.Name, &b.Template, &b.SideSizeSet, &b.Position, &b.CreatedAt); err != nil {
		return nil, err
	}
	return b, nil
}

func insertBoard(ctx context.Context, tx pgx.Tx, b Leaderboard) (*Leaderboard, error) {
	return scanBoard(tx.QueryRow(ctx,
		`INSERT INTO leaderboards AS b (tally_id, slug, name, template, side_size, position)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+boardCols,
		b.TallyID, b.Slug, b.Name, b.Template, b.SideSizeSet, b.Position))
}

// ListLeaderboards lists the tally's leaderboards, the default first.
func (s *Store) ListLeaderboards(ctx context.Context, tallyID string) ([]Leaderboard, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+boardCols+` FROM leaderboards b WHERE b.tally_id = $1`+boardOrder, tallyID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Leaderboard, error) {
		b, err := scanBoard(row)
		if err != nil {
			return Leaderboard{}, err
		}
		return *b, nil
	})
}

// DefaultLeaderboard is the tally's first leaderboard, the one a link
// without ?board= shows.
func (s *Store) DefaultLeaderboard(ctx context.Context, tallyID string) (*Leaderboard, error) {
	return scanBoard(s.pool.QueryRow(ctx, `SELECT `+boardCols+` FROM leaderboards b WHERE b.tally_id = $1`+boardOrder+` LIMIT 1`, tallyID))
}
