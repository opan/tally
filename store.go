package main

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the data-access layer over PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

var ErrUsernameTaken = errors.New("username taken")

type User struct {
	ID        string
	Username  string
	CreatedAt time.Time
}

// newToken returns an unguessable, URL-safe token (~26 lowercase chars).
func newToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing is not recoverable
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	return strings.ToLower(enc.EncodeToString(b))
}

func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// --- Users & sessions ---

func (s *Store) CreateUser(ctx context.Context, username, passwordHash string) (*User, error) {
	u := &User{Username: username}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash) VALUES ($1, $2)
		 RETURNING id, created_at`, username, passwordHash,
	).Scan(&u.ID, &u.CreatedAt)
	if isUniqueViolation(err) {
		return nil, ErrUsernameTaken
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// UserByUsername returns the user and its password hash, for login.
func (s *Store) UserByUsername(ctx context.Context, username string) (*User, string, error) {
	u := &User{}
	var hash string
	err := s.pool.QueryRow(ctx,
		`SELECT id, username, COALESCE(password_hash, ''), created_at FROM users WHERE username = $1`, username,
	).Scan(&u.ID, &u.Username, &hash, &u.CreatedAt)
	if err != nil {
		return nil, "", err
	}
	return u, hash, nil
}

func (s *Store) CreateSession(ctx context.Context, token, userID string, expires time.Time, idToken string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO sessions (id, user_id, expires_at, id_token) VALUES ($1, $2, $3, NULLIF($4, ''))`,
		token, userID, expires, idToken)
	return err
}

// SessionIDToken returns the aio ID token stored with a session ("" if none).
func (s *Store) SessionIDToken(ctx context.Context, token string) (string, error) {
	var idToken string
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(id_token, '') FROM sessions WHERE id = $1`, token).Scan(&idToken)
	return idToken, err
}

func (s *Store) UserBySession(ctx context.Context, token string) (*User, error) {
	u := &User{}
	err := s.pool.QueryRow(ctx,
		`SELECT u.id, u.username, u.created_at
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.id = $1 AND s.expires_at > now()`, token,
	).Scan(&u.ID, &u.Username, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// --- Login through all-in-one (OIDC) ---

// UserByAioID returns the local user linked to an aio account; pgx.ErrNoRows
// if none is linked yet.
func (s *Store) UserByAioID(ctx context.Context, aioID string) (*User, error) {
	u := &User{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, username, created_at FROM users WHERE aio_user_id = $1`, aioID,
	).Scan(&u.ID, &u.Username, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// CreateAioUser creates a local user linked to an aio account. It has no
// local password. ErrUsernameTaken if the username is in use.
func (s *Store) CreateAioUser(ctx context.Context, aioID, username string) (*User, error) {
	u := &User{Username: username}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO users (username, aio_user_id) VALUES ($1, $2) RETURNING id, created_at`,
		username, aioID,
	).Scan(&u.ID, &u.CreatedAt)
	if isUniqueViolation(err) {
		return nil, ErrUsernameTaken
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// UsernameOwner reports whether a username exists and, if so, which aio
// account it is linked to ("" for a local-only account).
func (s *Store) UsernameOwner(ctx context.Context, username string) (exists bool, aioID string, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT COALESCE(aio_user_id, '') FROM users WHERE username = $1`, username).Scan(&aioID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", nil
	}
	return err == nil, aioID, err
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, token)
	return err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	ct, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	return ct.RowsAffected(), err
}

// Totals are app-wide counts for the metrics gauges.
type Totals struct {
	Users, Tallies, ActiveTallies, Players, Matches, PlayDays int64
}

func (s *Store) Totals(ctx context.Context) (Totals, error) {
	var t Totals
	err := s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM users),
		(SELECT count(*) FROM tallies),
		(SELECT count(DISTINCT p.tally_id) FROM rounds r JOIN periods p ON p.id = r.period_id
		  WHERE r.played_on >= current_date - 30),
		(SELECT count(*) FROM participants WHERE active),
		(SELECT count(*) FROM matches WHERE status = 'done'),
		(SELECT count(*) FROM rounds)`,
	).Scan(&t.Users, &t.Tallies, &t.ActiveTallies, &t.Players, &t.Matches, &t.PlayDays)
	return t, err
}
