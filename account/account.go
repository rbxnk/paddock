// Package account keeps the accounts (users): who they are, not how they sign in (auth).
package account

import (
	"context"
	"database/sql"
	"errors"

	"github.com/rbxnk/paddock/auth"
	"github.com/rbxnk/paddock/store"
)

// Schema creates the table; the app's migration runs it, before auth's.
const Schema = `
CREATE TABLE users (
	id        TEXT PRIMARY KEY NOT NULL,
	email     TEXT DEFAULT '' NOT NULL,
	verified  BOOLEAN DEFAULT FALSE NOT NULL,
	superuser BOOLEAN DEFAULT FALSE NOT NULL,
	created   TEXT DEFAULT '' NOT NULL,
	updated   TEXT DEFAULT '' NOT NULL
);
CREATE UNIQUE INDEX idx_users_email ON users (email) WHERE email != '';
`

// User is an account.
type User struct {
	ID        string
	Email     string
	Verified  bool
	Superuser bool
	Created   string
	Updated   string
}

var ErrNotFound = errors.New("user not found")

// Store keeps accounts in DB. Audit, if set, runs after each write, in its transaction.
type Store struct {
	DB    *store.Store
	Audit func(ctx context.Context, action string, u User) error
}

const cols = `id, email, verified, superuser, created, updated`

func scan(row *sql.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.Verified, &u.Superuser, &u.Created, &u.Updated)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return u, err
}

// Get is the account with this id, or ErrNotFound.
func (s Store) Get(ctx context.Context, id string) (User, error) {
	r, _ := s.DB.Conn(ctx)
	return scan(r.QueryRowContext(ctx, `SELECT `+cols+` FROM users WHERE id = ?`, id))
}

// ByEmail is the account with this email, or ErrNotFound.
func (s Store) ByEmail(ctx context.Context, email string) (User, error) {
	r, _ := s.DB.Conn(ctx)
	return scan(r.QueryRowContext(ctx, `SELECT `+cols+` FROM users WHERE email = ?`, email))
}

// All lists every account, oldest first.
func (s Store) All(ctx context.Context) ([]User, error) {
	r, _ := s.DB.Conn(ctx)
	rows, err := r.QueryContext(ctx, `SELECT `+cols+` FROM users ORDER BY created`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.Verified, &u.Superuser, &u.Created, &u.Updated); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CheckPassword is the account with this email if password is theirs, else ErrNotFound.
func (s Store) CheckPassword(ctx context.Context, email, password string) (User, error) {
	u, err := s.ByEmail(ctx, email)
	if err != nil {
		return u, err
	}
	if ok, err := s.auth(ctx).CheckPassword(ctx, u.ID, password); err != nil {
		return u, err
	} else if !ok {
		return u, ErrNotFound
	}
	return u, nil
}

// SetPassword sets the account's password; the link that let them do it reached them at
// their address, so it's verified too.
func (s Store) SetPassword(ctx context.Context, userId, password string) error {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	return s.DB.Tx(ctx, func(ctx context.Context) error {
		u, err := s.Get(ctx, userId)
		if err != nil {
			return err
		}
		u.Verified = true
		if err := s.Update(ctx, u); err != nil {
			return err
		}
		return s.auth(ctx).SetPassword(ctx, userId, hash)
	})
}

// Create adds the account, filling its id and times, with a password if hash (from
// auth.HashPassword) isn't "".
func (s Store) Create(ctx context.Context, u *User, hash string) error {
	u.ID, u.Created = store.NewId(), store.Now()
	u.Updated = u.Created
	return s.DB.Tx(ctx, func(ctx context.Context) error {
		_, w := s.DB.Conn(ctx)
		if _, err := w.ExecContext(ctx, `INSERT INTO users (`+cols+`) VALUES (?, ?, ?, ?, ?, ?)`,
			u.ID, u.Email, u.Verified, u.Superuser, u.Created, u.Updated); err != nil {
			return err
		}
		if err := s.audit(ctx, "create", *u); err != nil {
			return err
		}
		if hash == "" {
			return nil
		}
		return s.auth(ctx).SetPassword(ctx, u.ID, hash)
	})
}

// Update saves the account's email and flags, and bumps updated.
func (s Store) Update(ctx context.Context, u User) error {
	u.Updated = store.Now()
	return s.DB.Tx(ctx, func(ctx context.Context) error {
		_, w := s.DB.Conn(ctx)
		if _, err := w.ExecContext(ctx, `UPDATE users SET email = ?, verified = ?, superuser = ?, updated = ? WHERE id = ?`,
			u.Email, u.Verified, u.Superuser, u.Updated, u.ID); err != nil {
			return err
		}
		return s.audit(ctx, "update", u)
	})
}

func (s Store) auth(ctx context.Context) auth.Store {
	r, w := s.DB.Conn(ctx)
	return auth.Store{R: r, W: w}
}

func (s Store) audit(ctx context.Context, action string, u User) error {
	if s.Audit == nil {
		return nil
	}
	return s.Audit(ctx, action, u)
}
