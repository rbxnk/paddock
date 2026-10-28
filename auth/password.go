package auth

import (
	"context"
	"database/sql"
	"errors"

	"golang.org/x/crypto/bcrypt"
)

// MethodsSchema creates auth_methods: one row per way a user signs in. Today that's only a
// password; passkeys and Google or Apple sign-in get their own kinds later.
const MethodsSchema = `
CREATE TABLE auth_methods (
	id      TEXT PRIMARY KEY NOT NULL,
	user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	kind    TEXT NOT NULL,
	secret  TEXT NOT NULL, -- for a password, its bcrypt hash
	created INTEGER NOT NULL, -- unix seconds
	updated INTEGER NOT NULL
);
CREATE UNIQUE INDEX idx_auth_methods_password ON auth_methods (user_id) WHERE kind = 'password';
`

// HashPassword is password's bcrypt hash, for SetPassword. It's slow on purpose, so call it
// before starting a transaction.
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(h), err
}

// SetPassword sets userId's password to the one hashed by HashPassword.
func (s Store) SetPassword(ctx context.Context, userId, hash string) error {
	id, _ := newToken()
	t := now().Unix()
	_, err := s.W.ExecContext(ctx, `INSERT INTO auth_methods (id, user_id, kind, secret, created, updated) VALUES (?, ?, 'password', ?, ?, ?)
		ON CONFLICT (user_id) WHERE kind = 'password' DO UPDATE SET secret = excluded.secret, updated = excluded.updated`,
		id, userId, hash, t, t)
	return err
}

// CheckPassword reports whether password is userId's. A user without one never matches.
func (s Store) CheckPassword(ctx context.Context, userId, password string) (bool, error) {
	var hash string
	err := s.R.QueryRowContext(ctx, `SELECT secret FROM auth_methods WHERE user_id = ? AND kind = 'password'`, userId).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil, nil
}
