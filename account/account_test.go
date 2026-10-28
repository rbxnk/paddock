package account

import (
	"context"
	"errors"
	"testing"

	"github.com/rbxnk/paddock/auth"
	"github.com/rbxnk/paddock/store"
)

func TestAccounts(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.W.Exec(Schema + auth.MethodsSchema); err != nil {
		t.Fatal(err)
	}
	var audited []string
	s := Store{DB: db, Audit: func(ctx context.Context, action string, u User) error {
		if _, ok := store.TxFrom(ctx); !ok {
			t.Error("audit ran outside the write's transaction")
		}
		audited = append(audited, action+" "+u.Email)
		return nil
	}}

	hash, _ := auth.HashPassword("hunter2hunter2")
	a := User{Email: "a@x"}
	if err := s.Create(ctx, &a, hash); err != nil || a.ID == "" || a.Created == "" {
		t.Fatal(a, err)
	}
	if err := s.Create(ctx, &User{Email: "a@x"}, ""); err == nil {
		t.Error("a second account with the same email")
	}
	if u, err := s.CheckPassword(ctx, "a@x", "hunter2hunter2"); err != nil || u.ID != a.ID {
		t.Error("right password:", u, err)
	}
	if _, err := s.CheckPassword(ctx, "a@x", "wrong"); !errors.Is(err, ErrNotFound) {
		t.Error("wrong password:", err)
	}
	if _, err := s.Get(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Error("missing id:", err)
	}
	if err := s.SetPassword(ctx, a.ID, "newpassword1"); err != nil {
		t.Fatal(err)
	}
	if u, err := s.CheckPassword(ctx, "a@x", "newpassword1"); err != nil || !u.Verified {
		t.Error("after SetPassword:", u, err)
	}
	b := User{Email: "b@x"}
	if err := s.Create(ctx, &b, ""); err != nil {
		t.Fatal(err)
	}
	if all, err := s.All(ctx); err != nil || len(all) != 2 || all[0].ID != a.ID {
		t.Error("All:", all, err)
	}
	if got, want := audited, []string{"create a@x", "update a@x", "create b@x"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("audited %q, want %q", got, want)
	}
}
