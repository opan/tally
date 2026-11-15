package main

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Iuran (RFC-004): each play day has a fee per player, and admins record who
// paid it and how. A payment is always the full fee of its play day.

const (
	PayCash     = "cash"
	PayTransfer = "transfer"
	PayFree     = "free"
)

// payMethods are the ways to pay, in the order the form offers them.
var payMethods = []string{PayCash, PayTransfer, PayFree}

func validPayMethod(m string) bool {
	for _, x := range payMethods {
		if x == m {
			return true
		}
	}
	return false
}

var (
	ErrNoFee = errors.New("play day has no fee")
	ErrPaid  = errors.New("player already paid for this play day")
)

// maxFee caps a play day's fee per player (whole rupiah).
const maxFee = 10_000_000

type Payment struct {
	ID            string
	RoundID       string
	ParticipantID string
	Name          string
	Method        string
	Note          string
	CreatedAt     time.Time
}

// PaySummary adds up a play day's payments.
type PaySummary struct {
	Fee                  int64
	Cash, Transfer, Free int
}

func (s PaySummary) Count() int { return s.Cash + s.Transfer + s.Free }

// Paid is how many paid money (Gratis doesn't).
func (s PaySummary) Paid() int { return s.Cash + s.Transfer }

func (s PaySummary) Collected() int64 { return int64(s.Paid()) * s.Fee }

func (s *PaySummary) add(o PaySummary) {
	s.Cash += o.Cash
	s.Transfer += o.Transfer
	s.Free += o.Free
}

func summarize(fee int64, ps []Payment) PaySummary {
	s := PaySummary{Fee: fee}
	for _, p := range ps {
		switch p.Method {
		case PayCash:
			s.Cash++
		case PayTransfer:
			s.Transfer++
		case PayFree:
			s.Free++
		}
	}
	return s
}

// PayDay is one play day on the Iuran page.
type PayDay struct {
	Round    Round
	Payments []Payment // newest first
	Sum      PaySummary
}

// roundArchived reports whether the play day's season is archived; it locks
// the play day so its fee and payments change one at a time.
func roundArchived(ctx context.Context, tx pgx.Tx, tallyID, roundID string) (fee int64, archived bool, err error) {
	err = tx.QueryRow(ctx,
		`SELECT r.fee, p.archived_at IS NOT NULL FROM rounds r JOIN periods p ON p.id = r.period_id
		 WHERE p.tally_id = $1 AND r.id = $2 FOR UPDATE OF r`, tallyID, roundID).Scan(&fee, &archived)
	return fee, archived, err
}

// SetRoundFee sets a play day's fee per player. Payments already recorded
// count at the new fee, which is what fixing a typo should do.
func (s *Store) SetRoundFee(ctx context.Context, tallyID, actorID string, r *Round, fee int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		before, archived, err := roundArchived(ctx, tx, tallyID, r.ID)
		if err != nil {
			return err
		}
		if archived {
			return ErrArchived
		}
		if before == fee {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE rounds SET fee = $2 WHERE id = $1`, r.ID, fee); err != nil {
			return err
		}
		return audit(ctx, tx, tallyID, actorID, "update", "round", r.ID,
			map[string]any{"fee": before}, map[string]any{"fee": fee, "played_on": r.PlayedOn.Format(dateLayout)})
	})
}

// SuggestedFee is what a play day's fee field starts with: the fee of the
// leaderboard's latest earlier play day that has one, else its latest one;
// 0 if none has.
func (s *Store) SuggestedFee(ctx context.Context, boardID string, r *Round) (int64, error) {
	var fee int64
	err := s.pool.QueryRow(ctx,
		`SELECT r.fee FROM rounds r JOIN periods p ON p.id = r.period_id
		 WHERE p.leaderboard_id = $1 AND r.fee > 0 AND r.id <> $2
		 ORDER BY (r.played_on <= $3) DESC, r.played_on DESC LIMIT 1`, boardID, r.ID, r.PlayedOn).Scan(&fee)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return fee, err
}

const paymentSelect = `SELECT pay.id, pay.round_id, p.id, p.display_name, pay.method, pay.note, pay.created_at
	FROM payments pay JOIN participants p ON p.id = pay.participant_id `

func scanPayments(rows pgx.Rows) ([]Payment, error) {
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Payment, error) {
		var p Payment
		err := row.Scan(&p.ID, &p.RoundID, &p.ParticipantID, &p.Name, &p.Method, &p.Note, &p.CreatedAt)
		return p, err
	})
}

// RecordPayment records that a player paid the play day's fee. With newName
// set instead of participantID it adds that player first (ErrNameTaken if the
// name exists). ErrNoFee before the play day has a fee; ErrPaid, with the
// existing payment, when the player is already recorded; ErrArchived in an
// archived season; pgx.ErrNoRows when participantID isn't in the tally.
func (s *Store) RecordPayment(ctx context.Context, tallyID, actorID string, r *Round, participantID, newName, method, note string) (Payment, error) {
	var out Payment
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		fee, archived, err := roundArchived(ctx, tx, tallyID, r.ID)
		switch {
		case err != nil:
			return err
		case archived:
			return ErrArchived
		case fee <= 0:
			return ErrNoFee
		}
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
		} else if err := tx.QueryRow(ctx,
			`SELECT display_name FROM participants WHERE tally_id = $1 AND id = $2`,
			tallyID, participantID).Scan(&name); err != nil {
			return err
		}
		err = tx.QueryRow(ctx,
			`INSERT INTO payments (round_id, participant_id, method, note, recorded_by)
			 VALUES ($1, $2, $3, $4, $5) ON CONFLICT (round_id, participant_id) DO NOTHING
			 RETURNING id, created_at`, r.ID, participantID, method, note, actorID).Scan(&out.ID, &out.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			rows, err := tx.Query(ctx, paymentSelect+`WHERE pay.round_id = $1 AND pay.participant_id = $2`, r.ID, participantID)
			if err != nil {
				return err
			}
			if got, err := scanPayments(rows); err != nil {
				return err
			} else if len(got) == 1 {
				out = got[0]
			}
			return ErrPaid
		}
		if err != nil {
			return err
		}
		out.RoundID, out.ParticipantID, out.Name, out.Method, out.Note = r.ID, participantID, name, method, note
		return audit(ctx, tx, tallyID, actorID, "create", "payment", out.ID, nil,
			map[string]any{"player": name, "method": method, "note": note, "fee": fee, "played_on": r.PlayedOn.Format(dateLayout)})
	})
	return out, err
}

// UndoPayment removes a payment of this tally (a wrong tap, the wrong
// player); pgx.ErrNoRows if none. ErrArchived, with roundID set, if its
// season is archived.
func (s *Store) UndoPayment(ctx context.Context, tallyID, actorID, id string) (roundID string, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT pay.round_id FROM payments pay JOIN participants p ON p.id = pay.participant_id
			 WHERE pay.id = $2 AND p.tally_id = $1`, tallyID, id).Scan(&roundID); err != nil {
			return err
		}
		fee, archived, err := roundArchived(ctx, tx, tallyID, roundID)
		if err != nil {
			return err
		}
		if archived {
			return ErrArchived
		}
		var name, method, note string
		if err := tx.QueryRow(ctx,
			`DELETE FROM payments pay USING participants p
			 WHERE pay.id = $2 AND p.id = pay.participant_id AND p.tally_id = $1
			 RETURNING p.display_name, pay.method, pay.note`, tallyID, id).Scan(&name, &method, &note); err != nil {
			return err
		}
		return audit(ctx, tx, tallyID, actorID, "delete", "payment", id,
			map[string]any{"player": name, "method": method, "note": note, "fee": fee}, nil)
	})
	return roundID, err
}

// RoundPayments lists a play day's payments, newest first.
func (s *Store) RoundPayments(ctx context.Context, roundID string) ([]Payment, error) {
	rows, err := s.pool.Query(ctx, paymentSelect+`WHERE pay.round_id = $1 ORDER BY pay.created_at DESC, pay.id`, roundID)
	if err != nil {
		return nil, err
	}
	return scanPayments(rows)
}

// PaymentDays lists the leaderboard's play days that have a fee, newest
// first, each with its payments.
func (s *Store) PaymentDays(ctx context.Context, tallyID, boardID string) ([]PayDay, error) {
	rounds, err := s.pool.Query(ctx,
		roundSelect+`WHERE p.tally_id = $1 AND p.leaderboard_id = $2 AND r.fee > 0 ORDER BY r.played_on DESC`, tallyID, boardID)
	if err != nil {
		return nil, err
	}
	rs, err := pgx.CollectRows(rounds, func(row pgx.CollectableRow) (*Round, error) { return scanRound(row) })
	if err != nil || len(rs) == 0 {
		return nil, err
	}
	ids := make([]string, len(rs))
	for i, r := range rs {
		ids[i] = r.ID
	}
	rows, err := s.pool.Query(ctx, paymentSelect+`WHERE pay.round_id::text = ANY($1) ORDER BY pay.created_at DESC, pay.id`, ids)
	if err != nil {
		return nil, err
	}
	all, err := scanPayments(rows)
	if err != nil {
		return nil, err
	}
	byRound := map[string][]Payment{}
	for _, p := range all {
		byRound[p.RoundID] = append(byRound[p.RoundID], p)
	}
	days := make([]PayDay, len(rs))
	for i, r := range rs {
		ps := byRound[r.ID]
		days[i] = PayDay{Round: *r, Payments: ps, Sum: summarize(r.Fee, ps)}
	}
	return days, nil
}

// PlayerPayments lists a player's payments in a season, newest play day
// first.
func (s *Store) PlayerPayments(ctx context.Context, periodID, participantID string) ([]Payment, error) {
	rows, err := s.pool.Query(ctx, paymentSelect+`JOIN rounds r ON r.id = pay.round_id
		WHERE r.period_id = $1 AND pay.participant_id = $2 ORDER BY r.played_on DESC`, periodID, participantID)
	if err != nil {
		return nil, err
	}
	return scanPayments(rows)
}

// HasFees reports whether any of the tally's play days has a fee, which is
// when the Iuran page shows up.
func (s *Store) HasFees(ctx context.Context, tallyID string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM rounds r JOIN periods p ON p.id = r.period_id WHERE p.tally_id = $1 AND r.fee > 0)`,
		tallyID).Scan(&ok)
	return ok, err
}

// PaymentTotals adds up a tally's Iuran for the site admin: play days with
// a fee, payments by method, and the money collected.
type PaymentTotals struct {
	Days      int
	Sum       PaySummary // Fee unset: days' fees differ
	Collected int64
}

func (s *Store) PaymentTotals(ctx context.Context, tallyID string) (PaymentTotals, error) {
	var t PaymentTotals
	err := s.pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM rounds r JOIN periods p ON p.id = r.period_id WHERE p.tally_id = $1 AND r.fee > 0),
		        count(*) FILTER (WHERE pay.method = 'cash'),
		        count(*) FILTER (WHERE pay.method = 'transfer'),
		        count(*) FILTER (WHERE pay.method = 'free'),
		        COALESCE(sum(r.fee) FILTER (WHERE pay.method <> 'free'), 0)
		 FROM payments pay JOIN rounds r ON r.id = pay.round_id JOIN periods p ON p.id = r.period_id
		 WHERE p.tally_id = $1`, tallyID).Scan(&t.Days, &t.Sum.Cash, &t.Sum.Transfer, &t.Sum.Free, &t.Collected)
	return t, err
}
