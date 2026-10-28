package settings

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSettings(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", t.TempDir()+"/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(Schema); err != nil {
		t.Fatal(err)
	}
	var got App
	if ok, err := Get(ctx, db, "", "app", &got); ok || err != nil {
		t.Fatalf("missing key: %v %v", ok, err)
	}
	key := "0123456789abcdef0123456789abcdef"
	want := App{Name: "Paddock Place", SMTP: SMTP{Host: "smtp.example.com", Port: 587, Password: "secret"}}
	if err := Set(ctx, db, key, "app", want); err != nil {
		t.Fatal(err)
	}
	var raw string
	db.QueryRow(`SELECT value FROM settings`).Scan(&raw)
	if raw == "" || raw[0] == '{' {
		t.Fatalf("stored in the clear: %s", raw)
	}
	if ok, err := Get(ctx, db, key, "app", &got); !ok || err != nil || got != want {
		t.Fatalf("got %+v %v %v", got, ok, err)
	}
	if _, err := Get(ctx, db, "", "app", &got); err == nil {
		t.Fatal("read an encrypted value without the key")
	}
	if _, err := Get(ctx, db, "fedcba9876543210fedcba9876543210", "app", &got); err == nil {
		t.Fatal("read an encrypted value with the wrong key")
	}
	if err := Set(ctx, db, "", "app", want); err != nil { // without a key it's plain, and still reads with one
		t.Fatal(err)
	}
	if ok, err := Get(ctx, db, key, "app", &got); !ok || err != nil || got != want {
		t.Fatalf("plain value: %+v %v %v", got, ok, err)
	}
}
