package reqlog

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestLog(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(Schema); err != nil {
		t.Fatal(err)
	}
	l := &Log{W: db}
	l.Add("GET", "/reset?t=secret", 200, 1500*time.Microsecond)
	for range 450 { // more than one insert's worth
		l.Add("POST", "/x", 500, time.Millisecond)
	}
	// an old row, past retention
	db.Exec(`INSERT INTO request_log VALUES (?, 'GET', '/old', 200, 1)`, time.Now().Add(-Retention-time.Hour).UnixMilli())

	got, err := l.Since(ctx, db, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 451 {
		t.Fatalf("%d entries, want 451", len(got))
	}
	if e := got[0]; e.Path != "/reset" || e.Method != "GET" || e.Status != 200 || e.Ms != 1.5 {
		t.Errorf("first entry: %+v", e)
	}
	if err := l.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM request_log`).Scan(&n)
	if n != 451 {
		t.Errorf("%d rows after pruning, want 451", n)
	}
}
