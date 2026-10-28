// Package store opens an app's SQLite database and keeps the helpers its records share: the
// transaction in the context, who writes, ids, times and JSON blobs.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"time"

	_ "modernc.org/sqlite"
)

// Store is the app's database. R reads (a pool); W writes, on one connection, so writes
// queue instead of failing busy.
type Store struct{ R, W *sql.DB }

// pragmas: WAL with a 10s busy timeout, foreign keys on. busy_timeout first: it must be set
// before journal_mode, which can wait on a lock.
const pragmas = "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=journal_size_limit(200000000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=temp_store(MEMORY)&_pragma=cache_size(-32000)&_defensive=1"

// Open opens the SQLite database at path, creating it if it's missing.
func Open(path string) (*Store, error) {
	r, err := sql.Open("sqlite", path+pragmas)
	if err != nil {
		return nil, err
	}
	r.SetMaxOpenConns(120)
	r.SetMaxIdleConns(15)
	r.SetConnMaxIdleTime(3 * time.Minute)
	w, err := sql.Open("sqlite", path+pragmas)
	if err != nil {
		r.Close()
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxIdleTime(3 * time.Minute)
	if err := w.Ping(); err != nil { // creates the file, and fails early on a bad path
		r.Close()
		w.Close()
		return nil, err
	}
	return &Store{R: r, W: w}, nil
}

// Close closes both connection pools.
func (s *Store) Close() error { return errors.Join(s.R.Close(), s.W.Close()) }

type txKey struct{}

// DBTX runs queries; *sql.DB and *sql.Tx both do.
type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	PrepareContext(context.Context, string) (*sql.Stmt, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// TxFrom is the transaction ctx is in (from Tx), if any.
func TxFrom(ctx context.Context) (*sql.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(*sql.Tx)
	return tx, ok
}

// Conn is the transaction ctx is in, else the store's own connections.
func (s *Store) Conn(ctx context.Context) (r, w DBTX) {
	if tx, ok := TxFrom(ctx); ok {
		return tx, tx
	}
	return s.R, s.W
}

// Tx runs fn in a transaction on the writer, joining ctx's if it is already in one. fn's ctx
// carries the transaction (TxFrom), so the store calls it makes run inside it.
func (s *Store) Tx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := TxFrom(ctx); ok {
		return fn(ctx)
	}
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		return err
	}
	return tx.Commit()
}

type actorKey struct{}

// WithActor tags ctx's writes with who makes them ("users:<id>"), for their audit rows.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// Actor is who ctx's writes are by: "" outside a request (commands, migrations).
func Actor(ctx context.Context) string {
	a, _ := ctx.Value(actorKey{}).(string)
	return a
}

// Marshal encodes a data blob with JSON v2, so nil slices and maps are stored as [] and {},
// not null.
func Marshal(v any) ([]byte, error) {
	return json.Marshal(v, json.Deterministic(true), jsontext.AllowInvalidUTF8(true))
}

// Unmarshal decodes a data blob.
func Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// NewId makes a record id: 15 random lowercase letters and digits.
func NewId() string { return RandomString(15, "abcdefghijklmnopqrstuvwxyz0123456789") }

// Alphanumeric is RandomString's alphabet for tokens and codes.
const Alphanumeric = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// RandomString is n characters picked uniformly from alphabet (at most 256) with crypto/rand.
func RandomString(n int, alphabet string) string {
	limit := 256 - 256%len(alphabet) // bytes at or above it would favour the first characters
	out := make([]byte, 0, n)
	buf := make([]byte, n)
	for len(out) < n {
		rand.Read(buf) // never fails (crypto/rand)
		for _, c := range buf {
			if int(c) < limit && len(out) < n {
				out = append(out, alphabet[int(c)%len(alphabet)])
			}
		}
	}
	return string(out)
}

// TimeLayout is how created, updated and other times are stored, always UTC; it sorts as text.
const TimeLayout = "2006-01-02 15:04:05.000Z"

// Now is a created or updated value, so update guards compare equal.
func Now() string { return FormatTime(time.Now()) }

// FormatTime stores t in TimeLayout; the zero time is "".
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(TimeLayout)
}

// ParseTime reads a stored time ("" is the zero time). RFC 3339 is accepted too.
func ParseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(TimeLayout, s)
	if err != nil {
		if t, err2 := time.Parse(time.RFC3339Nano, s); err2 == nil {
			return t, nil
		}
	}
	return t, err
}
