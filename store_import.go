package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ImportDay is one play day of a sheet import: each player's points in the
// order they were played.
type ImportDay struct {
	Date  time.Time
	Plays []sheetPlays
}

// ImportResult counts what an import wrote.
type ImportResult struct {
	Days       int // play days written
	Skipped    int // play days left alone because they already had points
	Replaced   int // play days whose scores were replaced
	Entries    int // scores written
	NewPlayers int
}

// ImportScores writes a sheet import into season p in one transaction:
// missing players and play days are created, and each play's points become a
// score entry (RFC-001 §7.4). A play day that already has points is skipped,
// or with replace has its scores (not matches or extras) replaced. from/to
// widen the season when the sheet runs outside it.
func (s *Store) ImportScores(ctx context.Context, tallyID, actorID string, p *Period, days []ImportDay, replace bool, from, to time.Time, file string) (ImportResult, error) {
	var res ImportResult
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		res = ImportResult{}
		if from.Before(p.StartsOn) || to.After(p.EndsOn) {
			if _, err := tx.Exec(ctx, `UPDATE periods SET starts_on = $2, ends_on = $3 WHERE id = $1`, p.ID, from, to); err != nil {
				return err
			}
			if err := audit(ctx, tx, tallyID, actorID, "update", "period", p.ID,
				map[string]string{"starts_on": p.StartsOn.Format(dateLayout), "ends_on": p.EndsOn.Format(dateLayout)},
				map[string]string{"starts_on": from.Format(dateLayout), "ends_on": to.Format(dateLayout), "by": "import"}); err != nil {
				return err
			}
		}

		ids := map[string]string{} // lower(name) → participant id
		rows, err := tx.Query(ctx, `SELECT id, lower(display_name) FROM participants WHERE tally_id = $1`, tallyID)
		if err != nil {
			return err
		}
		var id, name string
		if _, err := pgx.ForEachRow(rows, []any{&id, &name}, func() error { ids[name] = id; return nil }); err != nil {
			return err
		}
		playerID := func(name string) (string, error) {
			key := strings.ToLower(name)
			if id, ok := ids[key]; ok {
				return id, nil
			}
			var id string
			err := tx.QueryRow(ctx,
				`INSERT INTO participants (tally_id, display_name) VALUES ($1, $2)
				 ON CONFLICT (tally_id, lower(display_name)) DO NOTHING RETURNING id`, tallyID, name).Scan(&id)
			if errors.Is(err, pgx.ErrNoRows) { // added by someone else meanwhile
				err = tx.QueryRow(ctx, `SELECT id FROM participants WHERE tally_id = $1 AND lower(display_name) = $2`, tallyID, key).Scan(&id)
			} else if err == nil {
				res.NewPlayers++
				err = audit(ctx, tx, tallyID, actorID, "create", "participant", id, nil, Participant{ID: id, Name: name, Active: true})
			}
			ids[key] = id
			return id, err
		}

		courts := 1
		if err := tx.QueryRow(ctx,
			`SELECT courts FROM rounds WHERE period_id = $1 ORDER BY played_on DESC LIMIT 1`, p.ID).Scan(&courts); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		for _, day := range days {
			var roundID string
			var existing int
			err := tx.QueryRow(ctx,
				`SELECT r.id, (SELECT count(*) FROM point_awards pa WHERE pa.round_id = r.id)
				 FROM rounds r WHERE r.period_id = $1 AND r.played_on = $2`, p.ID, day.Date).Scan(&roundID, &existing)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				if err := tx.QueryRow(ctx,
					`INSERT INTO rounds (period_id, played_on, courts) VALUES ($1, $2, $3) RETURNING id`,
					p.ID, day.Date, courts).Scan(&roundID); err != nil {
					return err
				}
			case err != nil:
				return err
			case existing > 0 && !replace:
				res.Skipped++
				continue
			case existing > 0:
				if _, err := tx.Exec(ctx, `DELETE FROM point_awards WHERE round_id = $1 AND kind = 'score'`, roundID); err != nil {
					return err
				}
				res.Replaced++
			}
			res.Days++
			for _, pl := range day.Plays {
				pid, err := playerID(pl.Name)
				if err != nil {
					return err
				}
				// Each play a millisecond apart keeps the sheet's slot order
				// (scores are listed by created_at).
				for i, pts := range pl.Points {
					if _, err := tx.Exec(ctx,
						`INSERT INTO point_awards (period_id, round_id, participant_id, points, kind, created_by, created_at)
						 VALUES ($1, $2, $3, $4, 'score', $5, now() + $6 * interval '1 millisecond')`,
						p.ID, roundID, pid, pts, actorID, i); err != nil {
						return err
					}
					res.Entries++
				}
				if _, err := tx.Exec(ctx,
					`INSERT INTO round_attendance (round_id, participant_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
					roundID, pid); err != nil {
					return err
				}
			}
		}
		return audit(ctx, tx, tallyID, actorID, "import", "sheet", p.ID, nil, map[string]any{
			"file": file, "days": res.Days, "skipped": res.Skipped, "replaced": res.Replaced,
			"entries": res.Entries, "new_players": res.NewPlayers,
		})
	})
	return res, err
}
