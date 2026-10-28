package auth

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newStore(t *testing.T) Store {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/t.db?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE users (id TEXT PRIMARY KEY); INSERT INTO users VALUES ('u1'), ('u2');` + Schema); err != nil {
		t.Fatal(err)
	}
	return Store{R: db, W: db}
}

// at sets the clock for the rest of the test.
func at(t *testing.T, tm time.Time) {
	old := now
	now = func() time.Time { return tm }
	t.Cleanup(func() { now = old })
}

func TestSessions(t *testing.T) {
	ctx, s := context.Background(), newStore(t)
	t0 := time.Unix(1_800_000_000, 0)
	at(t, t0)
	a, err := s.NewSession(ctx, "u1", 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.NewSession(ctx, "u1", 30*24*time.Hour)
	c, _ := s.NewSession(ctx, "u2", time.Hour)
	if u, err := s.Session(ctx, a); u != "u1" || err != nil {
		t.Fatalf("session: %q %v", u, err)
	}
	if _, err := s.Session(ctx, "made-up"); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown token: %v", err)
	}
	var stored int
	s.R.QueryRowContext(ctx, `SELECT count(*) FROM sessions WHERE id = ?`, a).Scan(&stored)
	if stored != 0 {
		t.Error("the token is stored as is")
	}

	// last_seen moves at most once an hour
	lastSeen := func() (v int64) {
		s.R.QueryRowContext(ctx, `SELECT last_seen FROM sessions WHERE id = ?`, hash(a)).Scan(&v)
		return v
	}
	at(t, t0.Add(59*time.Minute))
	s.Session(ctx, a)
	if lastSeen() != t0.Unix() {
		t.Error("last_seen moved within the hour")
	}
	at(t, t0.Add(61*time.Minute))
	s.Session(ctx, a)
	if lastSeen() != t0.Add(61*time.Minute).Unix() {
		t.Error("last_seen didn't move after an hour")
	}
	if _, err := s.Session(ctx, c); !errors.Is(err, ErrInvalid) {
		t.Errorf("expired session: %v", err)
	}

	if err := s.EndSession(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, a); !errors.Is(err, ErrInvalid) {
		t.Error("ended session still works")
	}
	if _, err := s.Session(ctx, b); err != nil {
		t.Error("ending one session ended another")
	}
	if n, _ := s.SessionCount(ctx, "u1"); n != 1 {
		t.Errorf("u1 has %d live sessions, want 1", n)
	}
	d, _ := s.NewSession(ctx, "u2", time.Hour)
	s.EndSessions(ctx, "u1")
	if _, err := s.Session(ctx, b); !errors.Is(err, ErrInvalid) {
		t.Error("EndSessions left a session")
	}
	if _, err := s.Session(ctx, d); err != nil {
		t.Error("EndSessions ended another user's session")
	}
	// a deleted user's sessions go with them
	s.W.ExecContext(ctx, `DELETE FROM users WHERE id = 'u2'`)
	if _, err := s.Session(ctx, d); !errors.Is(err, ErrInvalid) {
		t.Error("a deleted user's session works")
	}
}

func TestLinks(t *testing.T) {
	ctx, s := context.Background(), newStore(t)
	t0 := time.Unix(1_800_000_000, 0)
	at(t, t0)
	tok, err := s.NewLink(ctx, "u1", "reset", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if u, err := s.LinkUser(ctx, tok, "reset"); u != "u1" || err != nil {
		t.Fatalf("link user: %q %v", u, err)
	}
	if _, err := s.UseLink(ctx, tok, "signin"); !errors.Is(err, ErrInvalid) {
		t.Error("a reset link signs in")
	}
	if u, err := s.UseLink(ctx, tok, "reset"); u != "u1" || err != nil {
		t.Fatalf("use: %q %v", u, err)
	}
	if _, err := s.UseLink(ctx, tok, "reset"); !errors.Is(err, ErrInvalid) {
		t.Error("a link works twice")
	}
	if _, err := s.LinkUser(ctx, tok, "reset"); !errors.Is(err, ErrInvalid) {
		t.Error("a used link still shows")
	}

	late, _ := s.NewLink(ctx, "u1", "signin", 15*time.Minute)
	at(t, t0.Add(15*time.Minute))
	if _, err := s.UseLink(ctx, late, "signin"); !errors.Is(err, ErrInvalid) {
		t.Error("an expired link works")
	}

	old, _ := s.NewLink(ctx, "u1", "reset", time.Hour)
	other, _ := s.NewLink(ctx, "u2", "reset", time.Hour)
	s.EndLinks(ctx, "u1", "reset")
	if _, err := s.UseLink(ctx, old, "reset"); !errors.Is(err, ErrInvalid) {
		t.Error("EndLinks left a link")
	}
	if _, err := s.UseLink(ctx, other, "reset"); err != nil {
		t.Error("EndLinks ended another user's link")
	}
}

func TestLimiter(t *testing.T) {
	var l Limiter
	t0 := time.Unix(1_800_000_000, 0)
	at(t, t0)
	for i := range 3 {
		if !l.Allow("k", 3, time.Minute) {
			t.Fatalf("event %d refused", i+1)
		}
	}
	if l.Allow("k", 3, time.Minute) {
		t.Error("4th event within a minute allowed")
	}
	if !l.Allow("other", 3, time.Minute) {
		t.Error("keys share counts")
	}
	at(t, t0.Add(time.Minute))
	if !l.Allow("k", 3, time.Minute) {
		t.Error("refused after the window")
	}
	// Over and Add: only what's added counts
	if l.Over("f", 1, time.Minute) {
		t.Error("over with nothing added")
	}
	l.Add("f")
	if !l.Over("f", 1, time.Minute) {
		t.Error("not over after an add")
	}
}

func TestPassword(t *testing.T) {
	ctx, s := context.Background(), newStore(t)
	if _, err := s.W.ExecContext(ctx, MethodsSchema); err != nil {
		t.Fatal(err)
	}
	check := func(user, pw string, want bool) {
		t.Helper()
		if ok, err := s.CheckPassword(ctx, user, pw); err != nil || ok != want {
			t.Fatalf("CheckPassword(%s, %s) = %v, %v; want %v", user, pw, ok, err, want)
		}
	}
	check("u1", "", false) // no password set
	for _, pw := range []string{"first pass", "second pass"} {
		h, err := HashPassword(pw)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetPassword(ctx, "u1", h); err != nil {
			t.Fatal(err)
		}
	}
	check("u1", "second pass", true)
	check("u1", "first pass", false)
	check("u2", "second pass", false)
}
