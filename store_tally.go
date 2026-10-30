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
	ErrRoundsOutside = errors.New("play days fall outside the new dates")
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
	Template    string
	Visibility  string
	CreatedAt   time.Time
	Branding    Branding
	LogoVer     int64 // logo's update time (unix); 0 when there's no logo
	SideSizeSet *int  // chosen players per side (team presets); nil = preset's
}

// SideSize is how many players play on each side.
func (t *Tally) SideSize() int {
	if t.SideSizeSet != nil {
		return *t.SideSizeSet
	}
	return t.Preset().SideSize
}

// Branding is the poster's optional text. Empty fields fall back to defaults.
type Branding struct {
	Title1 string `json:"title1,omitempty"` // white line, e.g. "BALA BULU"
	Title2 string `json:"title2,omitempty"` // gold line, e.g. "POINT RANK"
	Badge  string `json:"badge,omitempty"`  // under the logo, e.g. "BALA BULU BC"
	Footer string `json:"footer,omitempty"` // e.g. "Play • Improve • Together"
}

func (t *Tally) HasLogo() bool { return t.LogoVer > 0 }

func (t *Tally) Preset() Preset {
	if p, ok := presetByID(t.Template); ok {
		return p
	}
	return presets[0]
}

// TallyCard is a tally as listed on the dashboard.
type TallyCard struct {
	Tally   Tally
	Role    string
	Players int
	Current *Period
}

type Participant struct {
	ID     string
	Name   string
	Active bool
}

type Period struct {
	ID       string
	TallyID  string
	Name     string
	StartsOn time.Time
	EndsOn   time.Time
	Scoring  ScoringConfig
}

// Season status by date, against today in Jakarta.
const (
	SeasonUpcoming = "upcoming"
	SeasonRunning  = "running"
	SeasonEnded    = "ended"
)

// Status is SeasonUpcoming, SeasonRunning or SeasonEnded. A season ends by
// its dates only; there is no separate "close".
func (p Period) Status() string {
	t := today()
	switch {
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

const tallyCols = `t.id, t.owner_id, t.slug, t.name, t.description, t.template, t.visibility, t.created_at, t.branding, t.side_size,
	COALESCE((SELECT extract(epoch FROM l.updated_at)::bigint FROM tally_logos l WHERE l.tally_id = t.id), 0)`

func scanTally(row pgx.Row, extra ...any) (*Tally, error) {
	t := &Tally{}
	dest := append([]any{&t.ID, &t.OwnerID, &t.Slug, &t.Name, &t.Description, &t.Template, &t.Visibility, &t.CreatedAt, &t.Branding, &t.SideSizeSet, &t.LogoVer}, extra...)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Store) CreateTally(ctx context.Context, ownerID string, t Tally) (*Tally, error) {
	var out *Tally
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		out, err = scanTally(tx.QueryRow(ctx,
			`INSERT INTO tallies AS t (owner_id, slug, name, description, template, visibility, side_size)
			 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING `+tallyCols,
			ownerID, t.Slug, t.Name, t.Description, t.Template, t.Visibility, t.SideSizeSet))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO tally_admins (tally_id, user_id, role) VALUES ($1, $2, $3)`,
			out.ID, ownerID, RoleOwner); err != nil {
			return err
		}
		return audit(ctx, tx, out.ID, ownerID, "create", "tally", out.ID, nil, out)
	})
	if isUniqueViolation(err) {
		return nil, ErrSlugTaken
	}
	return out, err
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
		p, err := s.CurrentPeriod(ctx, cards[i].Tally.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		cards[i].Current = p
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

const periodCols = `id, tally_id, name, starts_on, ends_on, scoring`

func scanPeriod(row pgx.Row) (*Period, error) {
	p := &Period{}
	var raw []byte
	if err := row.Scan(&p.ID, &p.TallyID, &p.Name, &p.StartsOn, &p.EndsOn, &raw); err != nil {
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

func (s *Store) ListPeriods(ctx context.Context, tallyID string) ([]Period, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+periodCols+` FROM periods WHERE tally_id = $1 ORDER BY starts_on DESC, created_at DESC`, tallyID)
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

// CurrentPeriod is the period containing today, else the most recently started
// one. pgx.ErrNoRows when the tally has no periods.
func (s *Store) CurrentPeriod(ctx context.Context, tallyID string) (*Period, error) {
	return scanPeriod(s.pool.QueryRow(ctx,
		`SELECT `+periodCols+` FROM periods WHERE tally_id = $1
		 ORDER BY ($2 BETWEEN starts_on AND ends_on) DESC, starts_on DESC, created_at DESC
		 LIMIT 1`, tallyID, today()))
}

func (s *Store) CreatePeriod(ctx context.Context, actorID string, p Period) (*Period, error) {
	var out *Period
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		out, err = scanPeriod(tx.QueryRow(ctx,
			`INSERT INTO periods (tally_id, name, starts_on, ends_on, scoring)
			 VALUES ($1, $2, $3, $4, $5::jsonb) RETURNING `+periodCols,
			p.TallyID, p.Name, p.StartsOn, p.EndsOn, scoringJSON(p.Scoring)))
		if err != nil {
			return err
		}
		return audit(ctx, tx, p.TallyID, actorID, "create", "period", out.ID, nil, out)
	})
	return out, err
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
