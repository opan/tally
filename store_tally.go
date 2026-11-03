package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrSlugTaken     = errors.New("slug taken")
	ErrNameTaken     = errors.New("name taken")
	ErrInUse         = errors.New("still referenced")
	ErrUserNotFound  = errors.New("user not found")
	ErrAlreadyAdmin  = errors.New("already admin")
	ErrNotOwner      = errors.New("not the owner")
	ErrRoundsOutside = errors.New("play days fall outside the new dates")
	ErrArchived      = errors.New("season is archived")
)

const (
	RoleOwner = "owner"
	RoleAdmin = "admin"

	VisibilityLink    = "link"    // anyone with the link can view
	VisibilityPrivate = "private" // admins only
)

type Tally struct {
	ID          string
	OwnerID     string
	Slug        string
	Name        string
	Description string
	Visibility  string
	CreatedAt   time.Time
	Branding    Branding
	LogoVer     int64 // logo's update time (unix); 0 when there's no logo
}

// Branding is the poster's optional text. Empty fields fall back to defaults.
type Branding struct {
	Title1 string `json:"title1,omitempty"` // white line, e.g. "BALA BULU"
	Title2 string `json:"title2,omitempty"` // gold line, e.g. "POINT RANK"
	Badge  string `json:"badge,omitempty"`  // under the logo, e.g. "BALA BULU BC"
	Footer string `json:"footer,omitempty"` // e.g. "Play • Improve • Together"
}

func (t *Tally) HasLogo() bool { return t.LogoVer > 0 }

// TallyCard is a tally as listed on the dashboard.
type TallyCard struct {
	Tally   Tally
	Boards  []Leaderboard // the default first
	Board   *Leaderboard  // the default one
	Role    string
	Players int
	Current *Period // the default leaderboard's
}

type Participant struct {
	ID     string
	Name   string
	Active bool
}

type Period struct {
	ID            string
	TallyID       string
	LeaderboardID string
	Name          string
	StartsOn      time.Time
	EndsOn        time.Time
	Scoring       ScoringConfig
	// ArchivedAt is set while the season is archived: read-only, left out
	// of the season pickers and never the current season.
	ArchivedAt *time.Time `json:",omitempty"`
}

func (p Period) Archived() bool { return p.ArchivedAt != nil }

// Season status by date, against today in Jakarta.
const (
	SeasonUpcoming = "upcoming"
	SeasonRunning  = "running"
	SeasonEnded    = "ended"
	SeasonArchived = "archived"
)

// Status is SeasonUpcoming, SeasonRunning or SeasonEnded by date, or
// SeasonArchived once the owner archived it.
func (p Period) Status() string {
	t := today()
	switch {
	case p.ArchivedAt != nil:
		return SeasonArchived
	case t.Before(p.StartsOn):
		return SeasonUpcoming
	case t.After(p.EndsOn):
		return SeasonEnded
	}
	return SeasonRunning
}

// Contains reports whether d falls inside the period (inclusive).
func (p *Period) Contains(d time.Time) bool {
	return !d.Before(p.StartsOn) && !d.After(p.EndsOn)
}

type Admin struct {
	UserID   string
	Username string
	Role     string
	AddedAt  time.Time
}

// isNotFound covers a missing row and a malformed id in the URL (Postgres
// rejects a non-uuid with invalid_text_representation); both mean 404.
func isNotFound(err error) bool {
	var pg *pgconn.PgError
	return errors.Is(err, pgx.ErrNoRows) || (errors.As(err, &pg) && pg.Code == "22P02")
}

func isFKViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23503"
}

// audit records a change inside the caller's transaction. before/after are
// JSON snapshots; nil becomes NULL.
func audit(ctx context.Context, tx pgx.Tx, tallyID, actorID, action, entity, entityID string, before, after any) error {
	enc := func(v any) (any, error) {
		if v == nil {
			return nil, nil
		}
		b, err := json.Marshal(v)
		return string(b), err
	}
	b, err := enc(before)
	if err != nil {
		return err
	}
	a, err := enc(after)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO audit_log (tally_id, actor_id, action, entity, entity_id, before, after)
		 VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb)`,
		tallyID, actorID, action, entity, entityID, b, a)
	return err
}

// --- Tallies ---

const tallyCols = `t.id, t.owner_id, t.slug, t.name, t.description, t.visibility, t.created_at, t.branding,
	COALESCE((SELECT extract(epoch FROM l.updated_at)::bigint FROM tally_logos l WHERE l.tally_id = t.id), 0)`

func scanTally(row pgx.Row, extra ...any) (*Tally, error) {
	t := &Tally{}
	dest := append([]any{&t.ID, &t.OwnerID, &t.Slug, &t.Name, &t.Description, &t.Visibility, &t.CreatedAt, &t.Branding, &t.LogoVer}, extra...)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	return t, nil
}

// CreateTally creates the tally with b (Template and SideSizeSet) as its
// first leaderboard.
func (s *Store) CreateTally(ctx context.Context, ownerID string, t Tally, b Leaderboard) (*Tally, error) {
	var out *Tally
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		// template and side_size are still written so an image from before
		// RFC-003 shows the right type after a rollback.
		out, err = scanTally(tx.QueryRow(ctx,
			`INSERT INTO tallies AS t (owner_id, slug, name, description, template, visibility, side_size)
			 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING `+tallyCols,
			ownerID, t.Slug, t.Name, t.Description, b.Template, t.Visibility, b.SideSizeSet))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO tally_admins (tally_id, user_id, role) VALUES ($1, $2, $3)`,
			out.ID, ownerID, RoleOwner); err != nil {
			return err
		}
		if err := audit(ctx, tx, out.ID, ownerID, "create", "tally", out.ID, nil, out); err != nil {
			return err
		}
		b.TallyID, b.Slug, b.Position = out.ID, boardSlug(b.Preset()), 0
		board, err := insertBoard(ctx, tx, b)
		if err != nil {
			return err
		}
		return audit(ctx, tx, out.ID, ownerID, "create", "leaderboard", board.ID, nil, board)
	})
	if isUniqueViolation(err) {
		return nil, ErrSlugTaken
	}
	return out, err
}

func (s *Store) TallyByID(ctx context.Context, id string) (*Tally, error) {
	return scanTally(s.pool.QueryRow(ctx, `SELECT `+tallyCols+` FROM tallies t WHERE t.id = $1`, id))
}

// TallyPlayerRow is one player on the site admin's tally page.
type TallyPlayerRow struct {
	Participant
	Plays      int // all seasons
	Points     int // all seasons
	LastPlayed *time.Time
}

// TallyPlayers lists a tally's players with their plays and points over all
// seasons, active first, by name.
func (s *Store) TallyPlayers(ctx context.Context, tallyID string) ([]TallyPlayerRow, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT p.id, p.display_name, p.active,
		        COUNT(pa.id) FILTER (WHERE pa.kind <> 'extra'), COALESCE(SUM(pa.points), 0), MAX(r.played_on)
		 FROM participants p
		 LEFT JOIN point_awards pa ON pa.participant_id = p.id
		 LEFT JOIN rounds r ON r.id = pa.round_id
		 WHERE p.tally_id = $1
		 GROUP BY p.id
		 ORDER BY p.active DESC, lower(p.display_name)`, tallyID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (TallyPlayerRow, error) {
		var t TallyPlayerRow
		err := row.Scan(&t.ID, &t.Name, &t.Active, &t.Plays, &t.Points, &t.LastPlayed)
		return t, err
	})
}

func (s *Store) TallyBySlug(ctx context.Context, slug string) (*Tally, error) {
	return scanTally(s.pool.QueryRow(ctx, `SELECT `+tallyCols+` FROM tallies t WHERE t.slug = $1`, slug))
}

// RoleFor returns the user's role on the tally, or "" if they aren't an admin.
func (s *Store) RoleFor(ctx context.Context, tallyID, userID string) (string, error) {
	var role string
	err := s.pool.QueryRow(ctx,
		`SELECT role FROM tally_admins WHERE tally_id = $1 AND user_id = $2`, tallyID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return role, err
}

func (s *Store) TalliesForUser(ctx context.Context, userID string) ([]TallyCard, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+tallyCols+`, ta.role,
		        (SELECT count(*) FROM participants p WHERE p.tally_id = t.id AND p.active)
		 FROM tally_admins ta JOIN tallies t ON t.id = ta.tally_id
		 WHERE ta.user_id = $1
		 ORDER BY t.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	var cards []TallyCard
	for rows.Next() {
		var c TallyCard
		t, err := scanTally(rows, &c.Role, &c.Players)
		if err != nil {
			rows.Close()
			return nil, err
		}
		c.Tally = *t
		cards = append(cards, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range cards {
		c := &cards[i]
		if c.Boards, err = s.ListLeaderboards(ctx, c.Tally.ID); err != nil {
			return nil, err
		}
		if len(c.Boards) == 0 {
			return nil, errors.New("tally has no leaderboard")
		}
		c.Board = &c.Boards[0]
		p, err := s.CurrentPeriod(ctx, c.Tally.ID, c.Board.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		c.Current = p
	}
	return cards, nil
}

func (s *Store) UpdateTally(ctx context.Context, actorID string, before *Tally, name, desc, visibility string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		after, err := scanTally(tx.QueryRow(ctx,
			`UPDATE tallies AS t SET name = $2, description = $3, visibility = $4
			 WHERE t.id = $1 RETURNING `+tallyCols,
			before.ID, name, desc, visibility))
		if err != nil {
			return err
		}
		return audit(ctx, tx, before.ID, actorID, "update", "tally", before.ID, before, after)
	})
}

func (s *Store) UpdateBranding(ctx context.Context, actorID string, t *Tally, b Branding) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE tallies SET branding = $2 WHERE id = $1`, t.ID, b); err != nil {
			return err
		}
		return audit(ctx, tx, t.ID, actorID, "update", "branding", t.ID, t.Branding, b)
	})
}

func (s *Store) SetLogo(ctx context.Context, actorID, tallyID, contentType string, data []byte) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO tally_logos (tally_id, content_type, data) VALUES ($1, $2, $3)
			 ON CONFLICT (tally_id) DO UPDATE SET content_type = $2, data = $3, updated_at = now()`,
			tallyID, contentType, data); err != nil {
			return err
		}
		return audit(ctx, tx, tallyID, actorID, "update", "logo", tallyID, nil,
			map[string]any{"content_type": contentType, "bytes": len(data)})
	})
}

func (s *Store) DeleteLogo(ctx context.Context, actorID, tallyID string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `DELETE FROM tally_logos WHERE tally_id = $1`, tallyID)
		if err != nil || ct.RowsAffected() == 0 {
			return err
		}
		return audit(ctx, tx, tallyID, actorID, "delete", "logo", tallyID, nil, nil)
	})
}

func (s *Store) Logo(ctx context.Context, tallyID string) (contentType string, data []byte, err error) {
	err = s.pool.QueryRow(ctx, `SELECT content_type, data FROM tally_logos WHERE tally_id = $1`, tallyID).Scan(&contentType, &data)
	return contentType, data, err
}

// --- Admins ---

func (s *Store) ListAdmins(ctx context.Context, tallyID string) ([]Admin, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT u.id, u.username, ta.role, ta.added_at
		 FROM tally_admins ta JOIN users u ON u.id = ta.user_id
		 WHERE ta.tally_id = $1
		 ORDER BY ta.role = 'owner' DESC, ta.added_at`, tallyID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Admin, error) {
		var a Admin
		err := row.Scan(&a.UserID, &a.Username, &a.Role, &a.AddedAt)
		return a, err
	})
}

func (s *Store) AddAdmin(ctx context.Context, tallyID, actorID, username string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var userID string
		err := tx.QueryRow(ctx, `SELECT id FROM users WHERE username = $1`, username).Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUserNotFound
		}
		if err != nil {
			return err
		}
		ct, err := tx.Exec(ctx,
			`INSERT INTO tally_admins (tally_id, user_id, role) VALUES ($1, $2, 'admin')
			 ON CONFLICT DO NOTHING`, tallyID, userID)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrAlreadyAdmin
		}
		return audit(ctx, tx, tallyID, actorID, "create", "admin", userID, nil, map[string]string{"username": username})
	})
}

// RemoveAdmin removes a non-owner admin. Returns pgx.ErrNoRows if there was none.
func (s *Store) RemoveAdmin(ctx context.Context, tallyID, actorID, userID string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var username string
		err := tx.QueryRow(ctx,
			`DELETE FROM tally_admins ta USING users u
			 WHERE ta.tally_id = $1 AND ta.user_id = $2 AND ta.role = 'admin' AND u.id = ta.user_id
			 RETURNING u.username`, tallyID, userID).Scan(&username)
		if err != nil {
			return err
		}
		return audit(ctx, tx, tallyID, actorID, "delete", "admin", userID, map[string]string{"username": username}, nil)
	})
}

// TransferOwnership makes one of the tally's admins its owner; the old owner
// stays on as a regular admin. Only the current owner may do it, checked
// again under the tally's row lock so two transfers can't both pass.
// Returns ErrUserNotFound if newOwnerID isn't an admin of this tally.
func (s *Store) TransferOwnership(ctx context.Context, tallyID, actorID, newOwnerID string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var ownerID string
		if err := tx.QueryRow(ctx, `SELECT owner_id FROM tallies WHERE id = $1 FOR UPDATE`, tallyID).Scan(&ownerID); err != nil {
			return err
		}
		if ownerID != actorID {
			return ErrNotOwner
		}
		var oldName, newName string
		err := tx.QueryRow(ctx,
			`SELECT o.username, n.username
			 FROM tally_admins ta JOIN users n ON n.id = ta.user_id, users o
			 WHERE ta.tally_id = $1 AND ta.user_id = $2 AND ta.role = 'admin' AND o.id = $3`,
			tallyID, newOwnerID, ownerID).Scan(&oldName, &newName)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUserNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE tally_admins SET role = CASE WHEN user_id = $2 THEN 'owner' ELSE 'admin' END
			 WHERE tally_id = $1 AND user_id IN ($2, $3)`, tallyID, newOwnerID, ownerID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE tallies SET owner_id = $2 WHERE id = $1`, tallyID, newOwnerID); err != nil {
			return err
		}
		return audit(ctx, tx, tallyID, actorID, "update", "owner", newOwnerID,
			map[string]string{"owner": oldName}, map[string]string{"owner": newName})
	})
}

// --- Participants ---

func (s *Store) ListParticipants(ctx context.Context, tallyID string) ([]Participant, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, display_name, active FROM participants
		 WHERE tally_id = $1 ORDER BY active DESC, lower(display_name)`, tallyID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Participant, error) {
		var p Participant
		err := row.Scan(&p.ID, &p.Name, &p.Active)
		return p, err
	})
}

func (s *Store) participant(ctx context.Context, q pgx.Tx, tallyID, id string) (*Participant, error) {
	p := &Participant{}
	err := q.QueryRow(ctx,
		`SELECT id, display_name, active FROM participants WHERE tally_id = $1 AND id = $2`,
		tallyID, id).Scan(&p.ID, &p.Name, &p.Active)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// AddParticipants inserts names that don't exist yet (case-insensitive) and
// returns the ones that were added and the ones skipped as duplicates.
func (s *Store) AddParticipants(ctx context.Context, tallyID, actorID string, names []string) (added, skipped []string, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		added, skipped = nil, nil
		for _, name := range names {
			var id string
			err := tx.QueryRow(ctx,
				`INSERT INTO participants (tally_id, display_name) VALUES ($1, $2)
				 ON CONFLICT (tally_id, lower(display_name)) DO NOTHING
				 RETURNING id`, tallyID, name).Scan(&id)
			if errors.Is(err, pgx.ErrNoRows) {
				skipped = append(skipped, name)
				continue
			}
			if err != nil {
				return err
			}
			if err := audit(ctx, tx, tallyID, actorID, "create", "participant", id, nil, Participant{ID: id, Name: name, Active: true}); err != nil {
				return err
			}
			added = append(added, name)
		}
		return nil
	})
	return added, skipped, err
}

func (s *Store) RenameParticipant(ctx context.Context, tallyID, actorID, id, name string) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		before, err := s.participant(ctx, tx, tallyID, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE participants SET display_name = $3 WHERE tally_id = $1 AND id = $2`, tallyID, id, name); err != nil {
			return err
		}
		after := *before
		after.Name = name
		return audit(ctx, tx, tallyID, actorID, "update", "participant", id, before, after)
	})
	if isUniqueViolation(err) {
		return ErrNameTaken
	}
	return err
}

func (s *Store) SetParticipantActive(ctx context.Context, tallyID, actorID, id string, active bool) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		before, err := s.participant(ctx, tx, tallyID, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE participants SET active = $3 WHERE tally_id = $1 AND id = $2`, tallyID, id, active); err != nil {
			return err
		}
		after := *before
		after.Active = active
		return audit(ctx, tx, tallyID, actorID, "update", "participant", id, before, after)
	})
}

// DeleteParticipant removes a player who has no matches or points yet;
// ErrInUse otherwise (deactivate them instead).
func (s *Store) DeleteParticipant(ctx context.Context, tallyID, actorID, id string) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		before, err := s.participant(ctx, tx, tallyID, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM participants WHERE tally_id = $1 AND id = $2`, tallyID, id); err != nil {
			return err
		}
		return audit(ctx, tx, tallyID, actorID, "delete", "participant", id, before, nil)
	})
	if isFKViolation(err) {
		return ErrInUse
	}
	return err
}

// --- Periods ---

const periodCols = `id, tally_id, leaderboard_id, name, starts_on, ends_on, scoring, archived_at`

func scanPeriod(row pgx.Row) (*Period, error) {
	p := &Period{}
	var raw []byte
	if err := row.Scan(&p.ID, &p.TallyID, &p.LeaderboardID, &p.Name, &p.StartsOn, &p.EndsOn, &raw, &p.ArchivedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &p.Scoring); err != nil {
		return nil, err
	}
	return p, nil
}

func scoringJSON(c ScoringConfig) string {
	b, _ := json.Marshal(c) // plain struct of ints and strings; can't fail
	return string(b)
}

// ListPeriods lists a leaderboard's seasons, newest first.
func (s *Store) ListPeriods(ctx context.Context, tallyID, boardID string) ([]Period, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+periodCols+` FROM periods WHERE tally_id = $1 AND leaderboard_id = $2
		 ORDER BY starts_on DESC, created_at DESC`, tallyID, boardID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Period, error) {
		p, err := scanPeriod(row)
		if err != nil {
			return Period{}, err
		}
		return *p, nil
	})
}

func (s *Store) PeriodByID(ctx context.Context, tallyID, id string) (*Period, error) {
	return scanPeriod(s.pool.QueryRow(ctx,
		`SELECT `+periodCols+` FROM periods WHERE tally_id = $1 AND id = $2`, tallyID, id))
}

// CurrentPeriod is the leaderboard's active period containing today, else the
// most recently started active one. pgx.ErrNoRows when it has none (archived
// seasons never count).
func (s *Store) CurrentPeriod(ctx context.Context, tallyID, boardID string) (*Period, error) {
	return scanPeriod(s.pool.QueryRow(ctx,
		`SELECT `+periodCols+` FROM periods WHERE tally_id = $1 AND leaderboard_id = $2 AND archived_at IS NULL
		 ORDER BY ($3 BETWEEN starts_on AND ends_on) DESC, starts_on DESC, created_at DESC
		 LIMIT 1`, tallyID, boardID, today()))
}

func (s *Store) CreatePeriod(ctx context.Context, actorID string, p Period) (*Period, error) {
	var out *Period
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		out, err = scanPeriod(tx.QueryRow(ctx,
			`INSERT INTO periods (tally_id, leaderboard_id, name, starts_on, ends_on, scoring)
			 VALUES ($1, $2, $3, $4, $5, $6::jsonb) RETURNING `+periodCols,
			p.TallyID, p.LeaderboardID, p.Name, p.StartsOn, p.EndsOn, scoringJSON(p.Scoring)))
		if err != nil {
			return err
		}
		return audit(ctx, tx, p.TallyID, actorID, "create", "period", out.ID, nil, out)
	})
	return out, err
}

// SetPeriodArchived archives or unarchives a season. Returns pgx.ErrNoRows if
// the tally has no such season or it is already in that state.
func (s *Store) SetPeriodArchived(ctx context.Context, tallyID, actorID, periodID string, archived bool) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var name string
		err := tx.QueryRow(ctx,
			`UPDATE periods SET archived_at = CASE WHEN $3 THEN now() END
			 WHERE tally_id = $1 AND id = $2 AND (archived_at IS NOT NULL) <> $3
			 RETURNING name`, tallyID, periodID, archived).Scan(&name)
		if err != nil {
			return err
		}
		action := "archive"
		if !archived {
			action = "unarchive"
		}
		return audit(ctx, tx, tallyID, actorID, action, "period", periodID, nil, map[string]string{"name": name})
	})
}

// UpdatePeriod saves a season. Once it has scored matches its format is
// locked (stored game scores wouldn't fit), other rule changes such as the win
// bonus rescore every match, and its dates must still cover all play days.
func (s *Store) UpdatePeriod(ctx context.Context, actorID string, before *Period, p Period) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var scored, outside int
		if err := tx.QueryRow(ctx,
			`SELECT (SELECT count(*) FROM matches WHERE period_id = $1 AND status = 'done'),
			        (SELECT count(*) FROM rounds WHERE period_id = $1 AND played_on NOT BETWEEN $2 AND $3)`,
			before.ID, p.StartsOn, p.EndsOn).Scan(&scored, &outside); err != nil {
			return err
		}
		if outside > 0 {
			return ErrRoundsOutside
		}
		if scored > 0 && (p.Scoring.Format != before.Scoring.Format || p.Scoring.Games != before.Scoring.Games) {
			return ErrScoringLocked
		}
		after, err := scanPeriod(tx.QueryRow(ctx,
			`UPDATE periods SET name = $3, starts_on = $4, ends_on = $5, scoring = $6::jsonb
			 WHERE tally_id = $1 AND id = $2 RETURNING `+periodCols,
			before.TallyID, before.ID, p.Name, p.StartsOn, p.EndsOn, scoringJSON(p.Scoring)))
		if err != nil {
			return err
		}
		if scored > 0 && after.Scoring != before.Scoring {
			if err := rescorePeriod(ctx, tx, before.ID, after.Scoring); err != nil {
				return err
			}
		}
		return audit(ctx, tx, before.TallyID, actorID, "update", "period", before.ID, before, after)
	})
}
