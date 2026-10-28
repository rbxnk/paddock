// Package auth keeps sign-in state: sessions behind a cookie token, single-use emailed links,
// and rate limits. It knows accounts only by user id; checking passwords and finding users
// is the app's job. Tokens are random and only their SHA-256 is stored, so a copy of the
// database can't be used to sign in.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

// Schema creates the tables; the app's migration runs it. Rows go when their user does.
const Schema = `
CREATE TABLE sessions (
	id        TEXT PRIMARY KEY NOT NULL, -- SHA-256 of the cookie token, hex
	user_id   TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	created   INTEGER NOT NULL, -- unix seconds
	last_seen INTEGER NOT NULL,
	expires   INTEGER NOT NULL
);
CREATE INDEX idx_sessions_user ON sessions (user_id);
CREATE TABLE auth_links (
	id      TEXT PRIMARY KEY NOT NULL, -- SHA-256 of the emailed token, hex
	user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	kind    TEXT NOT NULL,
	created INTEGER NOT NULL,
	expires INTEGER NOT NULL,
	used    INTEGER
);
CREATE INDEX idx_auth_links_user ON auth_links (user_id);
`

// DBTX runs queries; *sql.DB and *sql.Tx both do.
type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// ErrInvalid is a token that is unknown, expired or already used.
var ErrInvalid = errors.New("auth: invalid or expired token")

// Store reads with R and writes with W (for SQLite's single writer they can differ).
type Store struct{ R, W DBTX }

var now = time.Now // tests move the clock

// NewSession starts a session for userId lasting ttl, and returns its cookie token.
func (s Store) NewSession(ctx context.Context, userId string, ttl time.Duration) (string, error) {
	token, id := newToken()
	t := now().Unix()
	if _, err := s.W.ExecContext(ctx, `DELETE FROM sessions WHERE expires <= ?`, t); err != nil {
		return "", err
	}
	_, err := s.W.ExecContext(ctx, `INSERT INTO sessions (id, user_id, created, last_seen, expires) VALUES (?, ?, ?, ?, ?)`,
		id, userId, t, t, now().Add(ttl).Unix())
	return token, err
}

// Session is the user signed in with token. It marks the session seen, at most once an hour.
func (s Store) Session(ctx context.Context, token string) (string, error) {
	var userId string
	var lastSeen int64
	id, t := hash(token), now().Unix()
	err := s.R.QueryRowContext(ctx, `SELECT user_id, last_seen FROM sessions WHERE id = ? AND expires > ?`, id, t).Scan(&userId, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrInvalid
	} else if err != nil {
		return "", err
	}
	if t-lastSeen >= 3600 {
		if _, err := s.W.ExecContext(ctx, `UPDATE sessions SET last_seen = ? WHERE id = ?`, t, id); err != nil {
			return "", err
		}
	}
	return userId, nil
}

// EndSession signs out the session with token.
func (s Store) EndSession(ctx context.Context, token string) error {
	_, err := s.W.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, hash(token))
	return err
}

// EndSessions signs userId out everywhere.
func (s Store) EndSessions(ctx context.Context, userId string) error {
	_, err := s.W.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userId)
	return err
}

// SessionCount is how many live sessions userId has.
func (s Store) SessionCount(ctx context.Context, userId string) (int, error) {
	var n int
	err := s.R.QueryRowContext(ctx, `SELECT count(*) FROM sessions WHERE user_id = ? AND expires > ?`, userId, now().Unix()).Scan(&n)
	return n, err
}

// NewLink makes a single-use token of kind (the app's name for what the link does) for
// userId, valid for ttl.
func (s Store) NewLink(ctx context.Context, userId, kind string, ttl time.Duration) (string, error) {
	token, id := newToken()
	t := now().Unix()
	if _, err := s.W.ExecContext(ctx, `DELETE FROM auth_links WHERE expires <= ?`, t); err != nil {
		return "", err
	}
	_, err := s.W.ExecContext(ctx, `INSERT INTO auth_links (id, user_id, kind, created, expires) VALUES (?, ?, ?, ?, ?)`,
		id, userId, kind, t, now().Add(ttl).Unix())
	return token, err
}

// LinkUser is the user a valid link of kind is for, without using it up.
func (s Store) LinkUser(ctx context.Context, token, kind string) (string, error) {
	var userId string
	err := s.R.QueryRowContext(ctx, `SELECT user_id FROM auth_links WHERE id = ? AND kind = ? AND used IS NULL AND expires > ?`,
		hash(token), kind, now().Unix()).Scan(&userId)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrInvalid
	}
	return userId, err
}

// UseLink uses up a valid link of kind and returns its user. Of two concurrent uses, one wins.
func (s Store) UseLink(ctx context.Context, token, kind string) (string, error) {
	var userId string
	t := now().Unix()
	err := s.W.QueryRowContext(ctx, `UPDATE auth_links SET used = ? WHERE id = ? AND kind = ? AND used IS NULL AND expires > ? RETURNING user_id`,
		t, hash(token), kind, t).Scan(&userId)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrInvalid
	}
	return userId, err
}

// EndLinks voids userId's unused links of kind, so an older emailed link stops working.
func (s Store) EndLinks(ctx context.Context, userId, kind string) error {
	_, err := s.W.ExecContext(ctx, `UPDATE auth_links SET used = ? WHERE user_id = ? AND kind = ? AND used IS NULL`, now().Unix(), userId, kind)
	return err
}

// newToken is a random URL-safe token and the id it's stored under.
func newToken() (token, id string) {
	b := make([]byte, 32)
	rand.Read(b)
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hash(token)
}

func hash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
