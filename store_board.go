package main

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// maxBoards caps leaderboards per tally; a club has a handful of sports.
const maxBoards = 10

var (
	ErrLastBoard     = errors.New("a tally keeps at least one leaderboard")
	ErrTooManyBoards = errors.New("too many leaderboards")
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

func (b Leaderboard) Preset() Preset {
	if p, ok := presetByID(b.Template); ok {
		return p
	}
	return presets[0]
}

// SideSize is how many players play on each side.
func (b Leaderboard) SideSize() int {
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

// LeaderboardRow is a leaderboard on the settings page, with its season count.
type LeaderboardRow struct {
	Leaderboard
	Seasons int
}

// LeaderboardRows lists the tally's leaderboards, the default first.
func (s *Store) LeaderboardRows(ctx context.Context, tallyID string) ([]LeaderboardRow, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+boardCols+`, (SELECT count(*) FROM periods p WHERE p.leaderboard_id = b.id)
		FROM leaderboards b WHERE b.tally_id = $1`+boardOrder, tallyID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (LeaderboardRow, error) {
		var r LeaderboardRow
		b := &r.Leaderboard
		err := row.Scan(&b.ID, &b.TallyID, &b.Slug, &b.Name, &b.Template, &b.SideSizeSet, &b.Position, &b.CreatedAt, &r.Seasons)
		return r, err
	})
}

// lockBoards locks the tally's row, so leaderboard changes on one tally run
// one at a time, and returns its leaderboards in order.
func lockBoards(ctx context.Context, tx pgx.Tx, tallyID string) ([]Leaderboard, error) {
	if _, err := tx.Exec(ctx, `SELECT 1 FROM tallies WHERE id = $1 FOR UPDATE`, tallyID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT `+boardCols+` FROM leaderboards b WHERE b.tally_id = $1`+boardOrder, tallyID)
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

// renumber stores the leaderboards' order as positions 0, 1, 2…
func renumber(ctx context.Context, tx pgx.Tx, boards []Leaderboard) error {
	for i, b := range boards {
		if b.Position == i {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE leaderboards SET position = $2 WHERE id = $1`, b.ID, i); err != nil {
			return err
		}
	}
	return nil
}

// CreateLeaderboard adds a leaderboard (Name, Template, SideSizeSet) at the
// end of the list. Its slug comes from the name, else the type, made unique
// in the tally; it never changes, so ?board= links keep working.
func (s *Store) CreateLeaderboard(ctx context.Context, tallyID, actorID string, b Leaderboard) (*Leaderboard, error) {
	var out *Leaderboard
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		boards, err := lockBoards(ctx, tx, tallyID)
		if err != nil {
			return err
		}
		if len(boards) >= maxBoards {
			return ErrTooManyBoards
		}
		base := normalizeSlug(b.Name)
		if len(base) > 30 {
			base = strings.Trim(base[:30], "-")
		}
		if base == "" {
			base = boardSlug(b.Preset())
		}
		taken := map[string]bool{}
		for _, x := range boards {
			taken[x.Slug] = true
		}
		b.Slug = base
		for n := 2; taken[b.Slug]; n++ {
			b.Slug = base + "-" + strconv.Itoa(n)
		}
		b.TallyID, b.Position = tallyID, len(boards)
		if out, err = insertBoard(ctx, tx, b); err != nil {
			return err
		}
		return audit(ctx, tx, tallyID, actorID, "create", "leaderboard", out.ID, nil, out)
	})
	return out, err
}

// RenameLeaderboard sets a leaderboard's name; "" shows the type's name.
// pgx.ErrNoRows if the tally has no such leaderboard.
func (s *Store) RenameLeaderboard(ctx context.Context, tallyID, actorID, id, name string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var before string
		if err := tx.QueryRow(ctx, `SELECT name FROM leaderboards WHERE tally_id = $1 AND id = $2 FOR UPDATE`, tallyID, id).Scan(&before); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE leaderboards SET name = $3 WHERE tally_id = $1 AND id = $2`, tallyID, id, name); err != nil {
			return err
		}
		return audit(ctx, tx, tallyID, actorID, "update", "leaderboard", id,
			map[string]string{"name": before}, map[string]string{"name": name})
	})
}

// MoveLeaderboard moves a leaderboard one place up or down the list; the
// first one is the default. A move past either end changes nothing.
// pgx.ErrNoRows if the tally has no such leaderboard.
func (s *Store) MoveLeaderboard(ctx context.Context, tallyID, actorID, id string, up bool) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		boards, err := lockBoards(ctx, tx, tallyID)
		if err != nil {
			return err
		}
		i := indexOfBoard(boards, id)
		if i < 0 {
			return pgx.ErrNoRows
		}
		j := i + 1
		if up {
			j = i - 1
		}
		if j < 0 || j >= len(boards) {
			return nil
		}
		boards[i], boards[j] = boards[j], boards[i]
		if err := renumber(ctx, tx, boards); err != nil {
			return err
		}
		return audit(ctx, tx, tallyID, actorID, "update", "leaderboard", id,
			map[string]int{"position": i}, map[string]int{"position": j})
	})
}

// DeleteLeaderboard removes a leaderboard that has no seasons (ErrInUse
// otherwise; archiving covers "we stopped playing this"). The last one stays
// (ErrLastBoard). pgx.ErrNoRows if the tally has no such leaderboard.
func (s *Store) DeleteLeaderboard(ctx context.Context, tallyID, actorID, id string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		boards, err := lockBoards(ctx, tx, tallyID)
		if err != nil {
			return err
		}
		i := indexOfBoard(boards, id)
		switch {
		case i < 0:
			return pgx.ErrNoRows
		case len(boards) == 1:
			return ErrLastBoard
		}
		var seasons int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM periods WHERE leaderboard_id = $1`, id).Scan(&seasons); err != nil {
			return err
		}
		if seasons > 0 {
			return ErrInUse
		}
		if _, err := tx.Exec(ctx, `DELETE FROM leaderboards WHERE id = $1`, id); err != nil {
			return err
		}
		gone := boards[i]
		if err := renumber(ctx, tx, append(boards[:i:i], boards[i+1:]...)); err != nil {
			return err
		}
		return audit(ctx, tx, tallyID, actorID, "delete", "leaderboard", id, gone, nil)
	})
}

func indexOfBoard(boards []Leaderboard, id string) int {
	for i, b := range boards {
		if b.ID == id {
			return i
		}
	}
	return -1
}
