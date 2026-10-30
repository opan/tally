package main

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrRoundExists   = errors.New("round exists for that date")
	ErrScoringLocked = errors.New("format can't change once a season has matches")
	ErrPlayCap       = errors.New("player reached the plays-per-day cap")
)

const (
	StatusScheduled = "scheduled"
	StatusDone      = "done"
)

// querier is what both the pool and a transaction offer.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Round struct {
	ID       string
	PeriodID string
	Number   int // position by date within the period, 1-based
	PlayedOn time.Time
	Courts   int
	Done     int // scored matches, filled by RoundsForPeriod
	Awards   int // point awards of any kind, filled by RoundsForPeriod
	Scores   int // points entered without match details, filled by RoundsForPeriod
	WeekPart int // 1-based position among the season's play days in the same Week
	WeekDays int // how many play days the season has in that Week
}

// Week is the week of the month by date, like the club's sheet: days 1–7 are
// week 1, 8–14 week 2, 15–21 week 3, 22–28 week 4, 29–31 week 5.
func (r Round) Week() int { return (r.PlayedOn.Day()-1)/7 + 1 }

// WeekLabel names the play day, e.g. "4", or "4A" and "4B" when the week has
// two play days.
func (r Round) WeekLabel() string {
	label := strconv.Itoa(r.Week())
	if r.WeekDays > 1 && r.WeekPart >= 1 && r.WeekPart <= 26 {
		label += string(rune('A' + r.WeekPart - 1))
	}
	return label
}

type MatchSide struct {
	Players []Participant
	Games   []int
	Total   int
	Result  Result
	Points  int
}

func (s MatchSide) Won() bool { return s.Result == ResultWin }

type Match struct {
	ID        string
	PeriodID  string
	RoundID   string
	Status    string
	PlayedOn  time.Time
	Sides     [2]MatchSide
	CreatedAt time.Time
}

func (m *Match) Scheduled() bool { return m.Status == StatusScheduled }

// PlayerIDs lists every player in the match.
func (m *Match) PlayerIDs() []string {
	var ids []string
	for _, s := range m.Sides {
		for _, p := range s.Players {
			ids = append(ids, p.ID)
		}
	}
	return ids
}

type Standing struct {
	ParticipantID string
	Name          string
	Points        int
	Played        int // matches, plus points entered without match details
	Entries       int // all awards, matches and manual; "played" for counter tallies
}

func (s Standing) Avg() float64 {
	if s.Played == 0 {
		return 0
	}
	return float64(s.Points) / float64(s.Played)
}

// --- Rounds ---

// sameWeek matches r2 to the play days of r's season in r's Week (same
// month, same 7-day block of the month).
const sameWeek = `r2.period_id = r.period_id
	AND date_trunc('month', r2.played_on) = date_trunc('month', r.played_on)
	AND (extract(day FROM r2.played_on)::int - 1) / 7 = (extract(day FROM r.played_on)::int - 1) / 7`

// roundSelect computes the round's number from its date order in the period,
// and its place in its week.
const roundSelect = `SELECT r.id, r.period_id, r.played_on, r.courts,
	(SELECT count(*) FROM rounds r2 WHERE r2.period_id = r.period_id AND r2.played_on <= r.played_on),
	(SELECT count(*) FROM rounds r2 WHERE ` + sameWeek + ` AND r2.played_on <= r.played_on),
	(SELECT count(*) FROM rounds r2 WHERE ` + sameWeek + `)
	FROM rounds r JOIN periods p ON p.id = r.period_id `

func scanRound(row pgx.Row) (*Round, error) {
	r := &Round{}
	if err := row.Scan(&r.ID, &r.PeriodID, &r.PlayedOn, &r.Courts, &r.Number, &r.WeekPart, &r.WeekDays); err != nil {
		return nil, err
	}
	return r, nil
}

func (s *Store) RoundByID(ctx context.Context, tallyID, id string) (*Round, error) {
	return scanRound(s.pool.QueryRow(ctx, roundSelect+`WHERE p.tally_id = $1 AND r.id = $2`, tallyID, id))
}

func (s *Store) RoundByDate(ctx context.Context, tallyID string, d time.Time) (*Round, error) {
	return scanRound(s.pool.QueryRow(ctx,
		roundSelect+`WHERE p.tally_id = $1 AND r.played_on = $2 ORDER BY p.starts_on DESC LIMIT 1`, tallyID, d))
}

func (s *Store) RoundsForPeriod(ctx context.Context, periodID string) ([]Round, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT r.id, r.period_id, r.played_on, r.courts,
		        row_number() OVER (ORDER BY r.played_on),
		        (SELECT count(*) FROM matches m WHERE m.round_id = r.id AND m.status = 'done'),
		        (SELECT count(*) FROM point_awards pa WHERE pa.round_id = r.id),
		        (SELECT count(*) FROM point_awards pa WHERE pa.round_id = r.id AND pa.kind = 'score'),
		        (SELECT count(*) FROM rounds r2 WHERE `+sameWeek+` AND r2.played_on <= r.played_on),
		        (SELECT count(*) FROM rounds r2 WHERE `+sameWeek+`)
		 FROM rounds r WHERE r.period_id = $1 ORDER BY r.played_on DESC`, periodID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Round, error) {
		var r Round
		err := row.Scan(&r.ID, &r.PeriodID, &r.PlayedOn, &r.Courts, &r.Number, &r.Done, &r.Awards, &r.Scores, &r.WeekPart, &r.WeekDays)
		return r, err
	})
}

// PeriodForDate is the season covering d (the latest-starting one if seasons
// overlap). pgx.ErrNoRows when none does.
func (s *Store) PeriodForDate(ctx context.Context, tallyID string, d time.Time) (*Period, error) {
	return scanPeriod(s.pool.QueryRow(ctx,
		`SELECT `+periodCols+` FROM periods WHERE tally_id = $1 AND $2 BETWEEN starts_on AND ends_on
		 ORDER BY starts_on DESC, created_at DESC LIMIT 1`, tallyID, d))
}

func setAttendance(ctx context.Context, tx pgx.Tx, tallyID, roundID string, ids []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM round_attendance WHERE round_id = $1`, roundID); err != nil {
		return err
	}
	// The join keeps ids from other tallies out.
	_, err := tx.Exec(ctx,
		`INSERT INTO round_attendance (round_id, participant_id)
		 SELECT $1, p.id FROM participants p WHERE p.tally_id = $2 AND p.id::text = ANY($3)`,
		roundID, tallyID, ids)
	return err
}

func (s *Store) CreateRound(ctx context.Context, tallyID, actorID, periodID string, d time.Time, courts int, present []string) (string, error) {
	var id string
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO rounds (period_id, played_on, courts) VALUES ($1, $2, $3) RETURNING id`,
			periodID, d, courts).Scan(&id); err != nil {
			return err
		}
		if err := setAttendance(ctx, tx, tallyID, id, present); err != nil {
			return err
		}
		return audit(ctx, tx, tallyID, actorID, "create", "round", id, nil,
			map[string]any{"played_on": d.Format(dateLayout), "courts": courts, "present": present})
	})
	if isUniqueViolation(err) {
		return "", ErrRoundExists
	}
	return id, err
}

func (s *Store) UpdateRoundSetup(ctx context.Context, tallyID, actorID string, r *Round, courts int, present []string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		before, err := s.attendance(ctx, tx, r.ID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE rounds SET courts = $2 WHERE id = $1`, r.ID, courts); err != nil {
			return err
		}
		if err := setAttendance(ctx, tx, tallyID, r.ID, present); err != nil {
			return err
		}
		return audit(ctx, tx, tallyID, actorID, "update", "round", r.ID,
			map[string]any{"courts": r.Courts, "present": before},
			map[string]any{"courts": courts, "present": present})
	})
}

func (s *Store) attendance(ctx context.Context, q querier, roundID string) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT participant_id::text FROM round_attendance WHERE round_id = $1`, roundID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func (s *Store) Attendance(ctx context.Context, roundID string) ([]string, error) {
	return s.attendance(ctx, s.pool, roundID)
}

// --- Matches ---

// loadMatches fetches matches matching where (on alias m) with their players and sides.
func loadMatches(ctx context.Context, q querier, where string, args ...any) ([]*Match, error) {
	rows, err := q.Query(ctx,
		`SELECT m.id, m.period_id, m.round_id, m.status, m.played_on, m.created_at
		 FROM matches m JOIN periods p ON p.id = m.period_id WHERE `+where+` ORDER BY m.created_at`, args...)
	if err != nil {
		return nil, err
	}
	var matches []*Match
	byID := map[string]*Match{}
	for rows.Next() {
		m := &Match{}
		if err := rows.Scan(&m.ID, &m.PeriodID, &m.RoundID, &m.Status, &m.PlayedOn, &m.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		matches = append(matches, m)
		byID[m.ID] = m
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(matches) == 0 {
		return matches, err
	}
	ids := make([]string, 0, len(matches))
	for _, m := range matches {
		ids = append(ids, m.ID)
	}

	rows, err = q.Query(ctx,
		`SELECT mp.match_id, mp.side, p.id, p.display_name, p.active
		 FROM match_players mp JOIN participants p ON p.id = mp.participant_id
		 WHERE mp.match_id::text = ANY($1) ORDER BY lower(p.display_name)`, ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var matchID string
		var side int
		var p Participant
		if err := rows.Scan(&matchID, &side, &p.ID, &p.Name, &p.Active); err != nil {
			rows.Close()
			return nil, err
		}
		m := byID[matchID]
		m.Sides[side-1].Players = append(m.Sides[side-1].Players, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = q.Query(ctx,
		`SELECT match_id, side, games, total, result, points FROM match_sides WHERE match_id::text = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var matchID string
		var side int
		var ms MatchSide
		var games []int32
		if err := rows.Scan(&matchID, &side, &games, &ms.Total, &ms.Result, &ms.Points); err != nil {
			rows.Close()
			return nil, err
		}
		for _, g := range games {
			ms.Games = append(ms.Games, int(g))
		}
		m := byID[matchID]
		ms.Players = m.Sides[side-1].Players
		m.Sides[side-1] = ms
	}
	rows.Close()
	return matches, rows.Err()
}

func (s *Store) MatchesForRound(ctx context.Context, tallyID, roundID string) ([]*Match, error) {
	return loadMatches(ctx, s.pool, `p.tally_id = $1 AND m.round_id = $2`, tallyID, roundID)
}

func (s *Store) MatchByID(ctx context.Context, tallyID, id string) (*Match, error) {
	ms, err := loadMatches(ctx, s.pool, `p.tally_id = $1 AND m.id = $2`, tallyID, id)
	if err != nil {
		return nil, err
	}
	if len(ms) == 0 {
		return nil, pgx.ErrNoRows
	}
	return ms[0], nil
}

// matchSnapshot is the audit-log form of a match.
func matchSnapshot(m *Match) map[string]any {
	if m == nil {
		return nil
	}
	sides := make([]map[string]any, 2)
	for i, s := range m.Sides {
		names := make([]string, 0, len(s.Players))
		for _, p := range s.Players {
			names = append(names, p.Name)
		}
		sides[i] = map[string]any{"players": names, "games": s.Games, "points": s.Points, "result": s.Result}
	}
	return map[string]any{"status": m.Status, "played_on": m.PlayedOn.Format(dateLayout), "sides": sides}
}

func insertPlayers(ctx context.Context, tx pgx.Tx, matchID string, sides [2][]string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM match_players WHERE match_id = $1`, matchID); err != nil {
		return err
	}
	for i, ids := range sides {
		for _, pid := range ids {
			if _, err := tx.Exec(ctx,
				`INSERT INTO match_players (match_id, side, participant_id) VALUES ($1, $2, $3)`,
				matchID, i+1, pid); err != nil {
				return err
			}
		}
	}
	return nil
}

// ScheduleMatches puts suggested pairs on court (no score yet).
func (s *Store) ScheduleMatches(ctx context.Context, tallyID, actorID string, r *Round, groups [][2][]string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		for _, g := range groups {
			var id string
			if err := tx.QueryRow(ctx,
				`INSERT INTO matches (period_id, round_id, played_on, status, created_by, updated_by)
				 VALUES ($1, $2, $3, 'scheduled', $4, $4) RETURNING id`,
				r.PeriodID, r.ID, r.PlayedOn, actorID).Scan(&id); err != nil {
				return err
			}
			if err := insertPlayers(ctx, tx, id, g); err != nil {
				return err
			}
			after, err := loadMatches(ctx, tx, `m.id = $1`, id)
			if err != nil {
				return err
			}
			if err := audit(ctx, tx, tallyID, actorID, "create", "match", id, nil, matchSnapshot(after[0])); err != nil {
				return err
			}
		}
		return nil
	})
}

// MatchResult is a validated score for one match.
type MatchResult struct {
	Players [2][]string
	Games   [2][]int
	Outcome [2]SideOutcome
}

// SaveMatch scores a match and (re)writes its points. before is nil to create
// a new, already-played match in round r.
func (s *Store) SaveMatch(ctx context.Context, tallyID, actorID string, r *Round, before *Match, res MatchResult) (string, error) {
	var id string
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		action := "update"
		if before == nil {
			action = "create"
			if err := tx.QueryRow(ctx,
				`INSERT INTO matches (period_id, round_id, played_on, status, created_by, updated_by)
				 VALUES ($1, $2, $3, 'done', $4, $4) RETURNING id`,
				r.PeriodID, r.ID, r.PlayedOn, actorID).Scan(&id); err != nil {
				return err
			}
		} else {
			id = before.ID
			if _, err := tx.Exec(ctx,
				`UPDATE matches SET status = 'done', updated_by = $2, updated_at = now() WHERE id = $1`,
				id, actorID); err != nil {
				return err
			}
		}
		if err := insertPlayers(ctx, tx, id, res.Players); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM match_sides WHERE match_id = $1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM point_awards WHERE match_id = $1`, id); err != nil {
			return err
		}
		for i, o := range res.Outcome {
			if _, err := tx.Exec(ctx,
				`INSERT INTO match_sides (match_id, side, games, total, result, points) VALUES ($1, $2, $3, $4, $5, $6)`,
				id, i+1, res.Games[i], o.Total, o.Result, o.Points); err != nil {
				return err
			}
			for _, pid := range res.Players[i] {
				if _, err := tx.Exec(ctx,
					`INSERT INTO point_awards (period_id, round_id, participant_id, match_id, points, kind, created_by)
					 VALUES ($1, $2, $3, $4, $5, 'match', $6)`, r.PeriodID, r.ID, pid, id, o.Points, actorID); err != nil {
					return err
				}
			}
		}
		after, err := loadMatches(ctx, tx, `m.id = $1`, id)
		if err != nil {
			return err
		}
		return audit(ctx, tx, tallyID, actorID, action, "match", id, matchSnapshot(before), matchSnapshot(after[0]))
	})
	return id, err
}

func (s *Store) DeleteMatch(ctx context.Context, tallyID, actorID string, m *Match) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM matches WHERE id = $1`, m.ID); err != nil {
			return err
		}
		return audit(ctx, tx, tallyID, actorID, "delete", "match", m.ID, matchSnapshot(m), nil)
	})
}

// StandingsAsOf totals each player's points in a period from play days on or
// before asOf (all play days when asOf is zero). Order with sortStandings.
func (s *Store) StandingsAsOf(ctx context.Context, periodID string, asOf time.Time) ([]Standing, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT p.id, p.display_name, SUM(pa.points), COUNT(*) FILTER (WHERE pa.kind <> 'extra'), COUNT(*)
		 FROM point_awards pa
		 JOIN participants p ON p.id = pa.participant_id
		 LEFT JOIN rounds r ON r.id = pa.round_id
		 WHERE pa.period_id = $1 AND ($2::date IS NULL OR r.played_on IS NULL OR r.played_on <= $2)
		 GROUP BY p.id, p.display_name`, periodID, nullDate(asOf))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Standing, error) {
		var st Standing
		err := row.Scan(&st.ParticipantID, &st.Name, &st.Points, &st.Played, &st.Entries)
		return st, err
	})
}

// StandingsForRound totals each player's points on one play day only.
func (s *Store) StandingsForRound(ctx context.Context, roundID string) ([]Standing, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT p.id, p.display_name, SUM(pa.points), COUNT(*) FILTER (WHERE pa.kind <> 'extra'), COUNT(*)
		 FROM point_awards pa
		 JOIN participants p ON p.id = pa.participant_id
		 WHERE pa.round_id = $1
		 GROUP BY p.id, p.display_name`, roundID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Standing, error) {
		var st Standing
		err := row.Scan(&st.ParticipantID, &st.Name, &st.Points, &st.Played, &st.Entries)
		return st, err
	})
}

// DayStats sums up one play day for the season page.
type DayStats struct {
	Players   int    // players with any points that day
	Plays     int    // scores plus match plays
	Points    int    // all points, extras included
	TopName   string // most points that day (ties: by name)
	TopPoints int
}

// SeasonDayStats returns DayStats per play day (round id) of a season, and
// how many players have points in the season.
func (s *Store) SeasonDayStats(ctx context.Context, periodID string) (map[string]DayStats, int, error) {
	rows, err := s.pool.Query(ctx,
		`WITH per AS (
		   SELECT pa.round_id, pa.participant_id, SUM(pa.points) AS pts,
		          COUNT(*) FILTER (WHERE pa.kind <> 'extra') AS plays
		   FROM point_awards pa WHERE pa.period_id = $1 AND pa.round_id IS NOT NULL
		   GROUP BY pa.round_id, pa.participant_id)
		 SELECT per.round_id, count(*), sum(per.plays), sum(per.pts),
		        (array_agg(p.display_name ORDER BY per.pts DESC, lower(p.display_name)))[1], max(per.pts)
		 FROM per JOIN participants p ON p.id = per.participant_id
		 GROUP BY per.round_id`, periodID)
	if err != nil {
		return nil, 0, err
	}
	out := map[string]DayStats{}
	var id string
	var d DayStats
	if _, err := pgx.ForEachRow(rows, []any{&id, &d.Players, &d.Plays, &d.Points, &d.TopName, &d.TopPoints}, func() error {
		out[id] = d
		return nil
	}); err != nil {
		return nil, 0, err
	}
	var players int
	err = s.pool.QueryRow(ctx,
		`SELECT count(DISTINCT participant_id) FROM point_awards WHERE period_id = $1`, periodID).Scan(&players)
	return out, players, err
}

func nullDate(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// rescorePeriod recomputes every scored match in a period under cfg, keeping
// the stored game scores. Used when a season's rule (e.g. win bonus) changes.
func rescorePeriod(ctx context.Context, tx pgx.Tx, periodID string, cfg ScoringConfig) error {
	matches, err := loadMatches(ctx, tx, `m.period_id = $1 AND m.status = 'done'`, periodID)
	if err != nil {
		return err
	}
	for _, m := range matches {
		out, err := cfg.Score(m.Sides[0].Games, m.Sides[1].Games)
		if err != nil {
			return err
		}
		for i, o := range out {
			if _, err := tx.Exec(ctx,
				`UPDATE match_sides SET total = $3, result = $4, points = $5 WHERE match_id = $1 AND side = $2`,
				m.ID, i+1, o.Total, o.Result, o.Points); err != nil {
				return err
			}
		}
	}
	_, err = tx.Exec(ctx,
		`UPDATE point_awards pa SET points = ms.points
		 FROM match_players mp JOIN match_sides ms ON ms.match_id = mp.match_id AND ms.side = mp.side
		 WHERE pa.period_id = $1 AND pa.match_id = mp.match_id AND pa.participant_id = mp.participant_id`,
		periodID)
	return err
}

func (s *Store) PeriodHasMatches(ctx context.Context, periodID string) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM matches WHERE period_id = $1 AND status = 'done'`, periodID).Scan(&n)
	return n > 0, err
}

// --- Manual points ---

// Award kinds. A point_awards row with a match_id is AwardMatch.
const (
	AwardMatch = "match" // from a recorded match
	AwardScore = "score" // one match's points entered without match details (RFC-001)
	AwardExtra = "extra" // bonus, penalty, or a counter tally's points
)

// Award is a point entry made by hand: a score (AwardScore) or an extra
// (AwardExtra).
type Award struct {
	ID            string
	ParticipantID string
	Name          string
	Points        int
	Reason        string
	RoundID       string
	Kind          string
	CreatedAt     time.Time
}

// AwardInput is one player's points in a batch entry.
type AwardInput struct {
	ParticipantID string
	Points        int
}

// AddAwards records manual points for a play day. Ids outside the tally are
// dropped by the join.
func (s *Store) AddAwards(ctx context.Context, tallyID, actorID string, r *Round, entries []AwardInput, reason string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		for _, e := range entries {
			var id, name string
			err := tx.QueryRow(ctx,
				`INSERT INTO point_awards (period_id, round_id, participant_id, points, reason, created_by)
				 SELECT $1, $2, p.id, $4, $5, $6 FROM participants p WHERE p.tally_id = $7 AND p.id = $3
				 RETURNING id, (SELECT display_name FROM participants WHERE id = $3)`,
				r.PeriodID, r.ID, e.ParticipantID, e.Points, reason, actorID, tallyID).Scan(&id, &name)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if err := audit(ctx, tx, tallyID, actorID, "create", "award", id, nil,
				map[string]any{"player": name, "points": e.Points, "reason": reason, "played_on": r.PlayedOn.Format(dateLayout)}); err != nil {
				return err
			}
		}
		return nil
	})
}

func scanAwards(rows pgx.Rows) ([]Award, error) {
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Award, error) {
		var a Award
		err := row.Scan(&a.ID, &a.ParticipantID, &a.Name, &a.Points, &a.Reason, &a.RoundID, &a.Kind, &a.CreatedAt)
		return a, err
	})
}

const awardSelect = `SELECT pa.id, p.id, p.display_name, pa.points, pa.reason, pa.round_id, pa.kind, pa.created_at
	FROM point_awards pa JOIN participants p ON p.id = pa.participant_id `

// ManualAwards lists a play day's extras (bonuses, penalties, counter
// entries), newest first.
func (s *Store) ManualAwards(ctx context.Context, roundID string) ([]Award, error) {
	rows, err := s.pool.Query(ctx, awardSelect+`WHERE pa.round_id = $1 AND pa.kind = 'extra' ORDER BY pa.created_at DESC`, roundID)
	if err != nil {
		return nil, err
	}
	return scanAwards(rows)
}

// ScoreAwards lists a play day's scores entered without match details, in
// the order they were entered.
func (s *Store) ScoreAwards(ctx context.Context, roundID string) ([]Award, error) {
	rows, err := s.pool.Query(ctx, awardSelect+`WHERE pa.round_id = $1 AND pa.kind = 'score' ORDER BY pa.created_at, pa.id`, roundID)
	if err != nil {
		return nil, err
	}
	return scanAwards(rows)
}

// AddScore records one match's points for a player without match details
// (RFC-001). With newName set instead of participantID it adds that player
// first (ErrNameTaken if the name exists). The player counts as present.
// ErrPlayCap when the player already has maxPlays scores and matches that
// day; pgx.ErrNoRows when participantID isn't in the tally.
func (s *Store) AddScore(ctx context.Context, tallyID, actorID string, r *Round, participantID, newName string, points, maxPlays int) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var name string
		if newName != "" {
			err := tx.QueryRow(ctx,
				`INSERT INTO participants (tally_id, display_name) VALUES ($1, $2)
				 ON CONFLICT (tally_id, lower(display_name)) DO NOTHING
				 RETURNING id`, tallyID, newName).Scan(&participantID)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNameTaken
			}
			if err != nil {
				return err
			}
			name = newName
			if err := audit(ctx, tx, tallyID, actorID, "create", "participant", participantID, nil,
				Participant{ID: participantID, Name: name, Active: true}); err != nil {
				return err
			}
		} else {
			// The row lock keeps two quick taps from both passing the cap.
			if err := tx.QueryRow(ctx,
				`SELECT display_name FROM participants WHERE tally_id = $1 AND id = $2 FOR UPDATE`,
				tallyID, participantID).Scan(&name); err != nil {
				return err
			}
		}
		var plays int
		if err := tx.QueryRow(ctx,
			`SELECT (SELECT count(*) FROM point_awards WHERE round_id = $1 AND participant_id = $2 AND kind = 'score')
			      + (SELECT count(*) FROM match_players mp JOIN matches m ON m.id = mp.match_id
			         WHERE m.round_id = $1 AND mp.participant_id = $2)`, r.ID, participantID).Scan(&plays); err != nil {
			return err
		}
		if plays >= maxPlays {
			return ErrPlayCap
		}
		var id string
		if err := tx.QueryRow(ctx,
			`INSERT INTO point_awards (period_id, round_id, participant_id, points, kind, created_by)
			 VALUES ($1, $2, $3, $4, 'score', $5) RETURNING id`,
			r.PeriodID, r.ID, participantID, points, actorID).Scan(&id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO round_attendance (round_id, participant_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			r.ID, participantID); err != nil {
			return err
		}
		return audit(ctx, tx, tallyID, actorID, "create", "award", id, nil,
			map[string]any{"player": name, "points": points, "kind": AwardScore, "played_on": r.PlayedOn.Format(dateLayout)})
	})
}

// DeleteAward removes a score or extra of this tally; pgx.ErrNoRows if none.
func (s *Store) DeleteAward(ctx context.Context, tallyID, actorID, id string) (roundID, kind string, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var name, reason string
		var points int
		if err := tx.QueryRow(ctx,
			`DELETE FROM point_awards pa USING participants p
			 WHERE pa.id = $2 AND pa.match_id IS NULL AND p.id = pa.participant_id AND p.tally_id = $1
			 RETURNING pa.round_id, pa.kind, p.display_name, pa.points, pa.reason`, tallyID, id).Scan(&roundID, &kind, &name, &points, &reason); err != nil {
			return err
		}
		return audit(ctx, tx, tallyID, actorID, "delete", "award", id,
			map[string]any{"player": name, "points": points, "reason": reason, "kind": kind}, nil)
	})
	return roundID, kind, err
}

// --- Player pages ---

func (s *Store) ParticipantByID(ctx context.Context, tallyID, id string) (*Participant, error) {
	p := &Participant{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, display_name, active FROM participants WHERE tally_id = $1 AND id = $2`,
		tallyID, id).Scan(&p.ID, &p.Name, &p.Active)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// PlayerMatches lists a player's scored matches in a season, oldest first.
func (s *Store) PlayerMatches(ctx context.Context, tallyID, periodID, participantID string) ([]*Match, error) {
	return loadMatches(ctx, s.pool,
		`p.tally_id = $1 AND m.period_id = $2 AND m.status = 'done'
		 AND EXISTS (SELECT 1 FROM match_players mp WHERE mp.match_id = m.id AND mp.participant_id = $3)`,
		tallyID, periodID, participantID)
}

// PlayerAwards lists a player's manual entries in a season, oldest first.
func (s *Store) PlayerAwards(ctx context.Context, periodID, participantID string) ([]Award, error) {
	rows, err := s.pool.Query(ctx,
		awardSelect+`WHERE pa.period_id = $1 AND pa.participant_id = $2 AND pa.match_id IS NULL ORDER BY pa.created_at`,
		periodID, participantID)
	if err != nil {
		return nil, err
	}
	return scanAwards(rows)
}
