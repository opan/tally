package main

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Linking players to accounts (RFC-002). An admin invites a player's account
// by username; the username is never looked up when the invite is sent, so
// sending one can't tell whether an account exists. The account's owner sees
// it on their home page and accepts, declines or ignores the tally.

var (
	ErrInviteLimit   = errors.New("too many invites today")
	ErrPlayerLinked  = errors.New("player already linked")
	ErrInviteGone    = errors.New("invite no longer pending")
	ErrAlreadyMember = errors.New("account already linked to a player in this tally")
)

// PlayerLink is a player's link state as their tally's admins see it.
type PlayerLink struct {
	Username  string    // the linked account's, or the pending invite's as typed
	Linked    bool      // false: an invite is pending
	ExpiresAt time.Time // pending invites only
}

// IncomingInvite is a pending invite as its user sees it on the home page.
type IncomingInvite struct {
	ID         string
	TallyID    string
	TallyName  string
	TallySlug  string
	PlayerName string
	InvitedBy  string // the inviting admin's username; "" if their account is gone
	ExpiresAt  time.Time
}

// MyPlayer is a player linked to the viewer's account, for "Mabar saya".
type MyPlayer struct {
	Tally      Tally
	PlayerID   string
	PlayerName string
	Period     *Period // the current season; nil before the first one
	Rank       int     // 0 before their first points this season
	Of         int
	Points     int
}

// PlayerLinks returns the link state of a tally's linked and invited players,
// by participant id. A declined invite still shows as pending until it
// expires, exactly like one sent to a username nobody has: otherwise a quick
// decline would tell the admins that the account exists (RFC-002 §4.2).
func (s *Store) PlayerLinks(ctx context.Context, tallyID string) (map[string]PlayerLink, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT p.id, u.username, true, NULL::timestamptz
		 FROM participants p JOIN users u ON u.id = p.user_id
		 WHERE p.tally_id = $1
		 UNION ALL
		 SELECT i.participant_id, i.username, false, i.expires_at
		 FROM player_invites i JOIN participants p ON p.id = i.participant_id
		 WHERE i.tally_id = $1 AND i.status IN ('pending', 'declined') AND i.expires_at > now() AND p.user_id IS NULL`, tallyID)
	if err != nil {
		return nil, err
	}
	links := map[string]PlayerLink{}
	for rows.Next() {
		var id string
		var l PlayerLink
		var exp *time.Time
		if err := rows.Scan(&id, &l.Username, &l.Linked, &exp); err != nil {
			rows.Close()
			return nil, err
		}
		if exp != nil {
			l.ExpiresAt = *exp
		}
		links[id] = l
	}
	rows.Close()
	return links, rows.Err()
}

// CreateInvite invites the account named username to be participantID. It
// replaces the player's pending invite, if any, and never checks whether the
// username exists. ErrInviteLimit when the tally sent perDay invites in the
// last 24 hours; ErrPlayerLinked when the player has an account already.
func (s *Store) CreateInvite(ctx context.Context, tallyID, actorID, participantID, username string, ttl time.Duration, perDay int) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Serialize invites per tally, so the daily cap holds under concurrent sends.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM tallies WHERE id = $1 FOR UPDATE`, tallyID); err != nil {
			return err
		}
		var sent int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM player_invites WHERE tally_id = $1 AND created_at > now() - interval '24 hours'`,
			tallyID).Scan(&sent); err != nil {
			return err
		}
		if sent >= perDay {
			return ErrInviteLimit
		}
		var linked bool
		if err := tx.QueryRow(ctx,
			`SELECT user_id IS NOT NULL FROM participants WHERE tally_id = $1 AND id = $2 FOR UPDATE`,
			tallyID, participantID).Scan(&linked); err != nil {
			return err
		}
		if linked {
			return ErrPlayerLinked
		}
		if err := cancelInvites(ctx, tx, tallyID, actorID, participantID); err != nil {
			return err
		}
		var id string
		if err := tx.QueryRow(ctx,
			`INSERT INTO player_invites (tally_id, participant_id, username, invited_by, expires_at)
			 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			tallyID, participantID, username, actorID, time.Now().Add(ttl)).Scan(&id); err != nil {
			return err
		}
		return audit(ctx, tx, tallyID, actorID, "create", "invite", id, nil,
			map[string]string{"participant_id": participantID, "username": username})
	})
}

// cancelInvites cancels the player's open invites: pending ones (expired too)
// and declined ones, which admins still see as pending.
func cancelInvites(ctx context.Context, tx pgx.Tx, tallyID, actorID, participantID string) error {
	rows, err := tx.Query(ctx,
		`UPDATE player_invites i SET status = 'cancelled'
		 FROM player_invites old
		 WHERE old.id = i.id AND i.tally_id = $1 AND i.participant_id = $2
		   AND (i.status = 'pending' OR (i.status = 'declined' AND i.expires_at > now()))
		 RETURNING i.id, i.username, old.status`, tallyID, participantID)
	if err != nil {
		return err
	}
	type gone struct{ id, username, status string }
	var cancelled []gone
	for rows.Next() {
		var g gone
		if err := rows.Scan(&g.id, &g.username, &g.status); err != nil {
			rows.Close()
			return err
		}
		cancelled = append(cancelled, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, g := range cancelled {
		if err := audit(ctx, tx, tallyID, actorID, "update", "invite", g.id,
			map[string]string{"status": g.status, "username": g.username}, map[string]string{"status": "cancelled"}); err != nil {
			return err
		}
	}
	return nil
}

// CancelInvite cancels a player's open invite; it's a no-op without one.
func (s *Store) CancelInvite(ctx context.Context, tallyID, actorID, participantID string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := s.participant(ctx, tx, tallyID, participantID); err != nil {
			return err
		}
		return cancelInvites(ctx, tx, tallyID, actorID, participantID)
	})
}

// UnlinkPlayer removes a player's account link. The actor is a tally admin,
// or the linked user themselves (then pass their id as userID; "" for admins).
func (s *Store) UnlinkPlayer(ctx context.Context, tallyID, actorID, participantID, userID string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var username string
		err := tx.QueryRow(ctx,
			`UPDATE participants p SET user_id = NULL FROM users u
			 WHERE p.tally_id = $1 AND p.id = $2 AND u.id = p.user_id AND ($3 = '' OR u.id::text = $3)
			 RETURNING u.username`, tallyID, participantID, userID).Scan(&username)
		if err != nil {
			return err
		}
		return audit(ctx, tx, tallyID, actorID, "delete", "player_link", participantID,
			map[string]string{"username": username}, nil)
	})
}

// IncomingInvites are the user's pending, unexpired invites, leaving out
// tallies they ignore and players who got an account meanwhile.
func (s *Store) IncomingInvites(ctx context.Context, u *User) ([]IncomingInvite, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT i.id, t.id, t.name, t.slug, p.display_name, COALESCE(inv.username, ''), i.expires_at
		 FROM player_invites i
		 JOIN tallies t ON t.id = i.tally_id
		 JOIN participants p ON p.id = i.participant_id
		 LEFT JOIN users inv ON inv.id = i.invited_by
		 WHERE i.username = $1 AND i.status = 'pending' AND i.expires_at > now() AND p.user_id IS NULL
		   AND NOT EXISTS (SELECT 1 FROM tally_mutes m WHERE m.user_id = $2 AND m.tally_id = i.tally_id)
		 ORDER BY i.created_at`, u.Username, u.ID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (IncomingInvite, error) {
		var in IncomingInvite
		err := row.Scan(&in.ID, &in.TallyID, &in.TallyName, &in.TallySlug, &in.PlayerName, &in.InvitedBy, &in.ExpiresAt)
		return in, err
	})
}

// Invite answers.
const (
	InviteAccept  = "accept"
	InviteDecline = "decline"
	InviteMute    = "mute" // decline, and hide later invites from this tally
)

// RespondInvite accepts or declines one of u's invites and returns its tally's
// slug. ErrInviteGone when it isn't u's, isn't pending or has expired (all
// look the same); ErrAlreadyMember when accepting while u already has a
// player in that tally.
func (s *Store) RespondInvite(ctx context.Context, u *User, inviteID, answer string) (string, error) {
	var slug string
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var tallyID, participantID string
		err := tx.QueryRow(ctx,
			`SELECT i.tally_id, i.participant_id, t.slug FROM player_invites i JOIN tallies t ON t.id = i.tally_id
			 WHERE i.id = $1 AND i.username = $2 AND i.status = 'pending' AND i.expires_at > now()
			 FOR UPDATE OF i`, inviteID, u.Username).Scan(&tallyID, &participantID, &slug)
		if isNotFound(err) {
			return ErrInviteGone
		}
		if err != nil {
			return err
		}
		status := "declined"
		if answer == InviteAccept {
			status = "accepted"
			ct, err := tx.Exec(ctx,
				`UPDATE participants SET user_id = $3 WHERE tally_id = $1 AND id = $2 AND user_id IS NULL`,
				tallyID, participantID, u.ID)
			if isUniqueViolation(err) {
				return ErrAlreadyMember
			}
			if err != nil {
				return err
			}
			if ct.RowsAffected() == 0 {
				return ErrInviteGone // the player got an account meanwhile
			}
			if err := audit(ctx, tx, tallyID, u.ID, "create", "player_link", participantID, nil,
				map[string]string{"username": u.Username}); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE player_invites SET status = $2 WHERE id = $1`, inviteID, status); err != nil {
			return err
		}
		if err := audit(ctx, tx, tallyID, u.ID, "update", "invite", inviteID,
			map[string]string{"status": "pending"}, map[string]string{"status": status}); err != nil {
			return err
		}
		// Ignoring a tally is the user's own setting: the tally's audit log
		// shows only the decline, so its admins can't tell.
		if answer == InviteMute {
			_, err = tx.Exec(ctx,
				`INSERT INTO tally_mutes (user_id, tally_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, u.ID, tallyID)
		}
		return err
	})
	return slug, err
}

// MutedTally is a tally whose invites the user ignores.
type MutedTally struct {
	ID   string
	Name string
}

func (s *Store) MutedTallies(ctx context.Context, userID string) ([]MutedTally, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT t.id, t.name FROM tally_mutes m JOIN tallies t ON t.id = m.tally_id
		 WHERE m.user_id = $1 ORDER BY m.muted_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (MutedTally, error) {
		var m MutedTally
		err := row.Scan(&m.ID, &m.Name)
		return m, err
	})
}

// Unmute shows a tally's invites to the user again; a no-op if it wasn't muted.
func (s *Store) Unmute(ctx context.Context, userID, tallyID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM tally_mutes WHERE user_id = $1 AND tally_id = $2`, userID, tallyID)
	if isNotFound(err) {
		return nil
	}
	return err
}

// LinkedPlayerID is the player in the tally linked to the user, or "".
func (s *Store) LinkedPlayerID(ctx context.Context, tallyID, userID string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM participants WHERE tally_id = $1 AND user_id = $2`, tallyID, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// MyPlayers lists the players linked to the user, with their rank and points
// in each tally's current season.
func (s *Store) MyPlayers(ctx context.Context, userID string) ([]MyPlayer, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+tallyCols+`, p.id, p.display_name
		 FROM participants p JOIN tallies t ON t.id = p.tally_id
		 WHERE p.user_id = $1
		 ORDER BY lower(t.name)`, userID)
	if err != nil {
		return nil, err
	}
	var out []MyPlayer
	for rows.Next() {
		var m MyPlayer
		t, err := scanTally(rows, &m.PlayerID, &m.PlayerName)
		if err != nil {
			rows.Close()
			return nil, err
		}
		m.Tally = *t
		out = append(out, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		m := &out[i]
		p, err := s.CurrentPeriod(ctx, m.Tally.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		m.Period = p
		st, err := s.StandingsAsOf(ctx, p.ID, time.Time{})
		if err != nil {
			return nil, err
		}
		if m.Tally.Preset().IsCounter() {
			useEntries(st)
		}
		sortStandings(st)
		m.Of = len(st)
		for j, row := range st {
			if row.ParticipantID == m.PlayerID {
				m.Rank, m.Points = j+1, row.Points
			}
		}
	}
	return out, nil
}

// TallyOfMyPlayer is the tally of a player linked to the user;
// pgx.ErrNoRows when the player isn't theirs.
func (s *Store) TallyOfMyPlayer(ctx context.Context, userID, participantID string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`SELECT tally_id FROM participants WHERE id = $1 AND user_id = $2`, participantID, userID).Scan(&id)
	return id, err
}
