// Package reqlog keeps a log of requests (time, method, path, status, duration) in the
// database for a week, for traffic and response-time summaries. Paths are stored without
// their query string, so tokens in links never land in it.
package reqlog

import (
	"context"
	"database/sql"
	"log"
	"strings"
	"sync"
	"time"
)

// Schema creates the table; the app's migration runs it.
const Schema = `
CREATE TABLE request_log (
	created INTEGER NOT NULL, -- unix milliseconds
	method  TEXT NOT NULL,
	path    TEXT NOT NULL,
	status  INTEGER NOT NULL,
	ms      REAL NOT NULL
);
CREATE INDEX idx_request_log_created ON request_log (created);
`

// Retention is how long rows are kept.
const Retention = 7 * 24 * time.Hour

// DBTX runs queries; *sql.DB and *sql.Tx both do.
type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// Entry is one logged request.
type Entry struct {
	Time   time.Time
	Method string
	Path   string
	Status int
	Ms     float64
}

// Log buffers entries in memory and writes them in batches, so a request never waits on a
// write and SQLite's single writer sees one insert every few seconds instead of one per request.
// shortcut: entries still in the buffer are lost if the process dies; fine for traffic stats.
type Log struct {
	W   DBTX // writes, on the app's single writer
	mu  sync.Mutex
	buf []Entry
}

// Add buffers one request. The path loses its query string.
func (l *Log) Add(method, path string, status int, d time.Duration) {
	path, _, _ = strings.Cut(path, "?")
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buf) < 100_000 { // a stalled writer drops entries rather than grow without bound
		l.buf = append(l.buf, Entry{time.Now(), method, path, status, float64(d) / float64(time.Millisecond)})
	}
}

// Flush writes the buffered entries.
func (l *Log) Flush(ctx context.Context) error {
	l.mu.Lock()
	buf := l.buf
	l.buf = nil
	l.mu.Unlock()
	for len(buf) > 0 {
		n := min(len(buf), 200) // 5 parameters each, well under SQLite's limit
		q := "INSERT INTO request_log (created, method, path, status, ms) VALUES " + strings.Repeat("(?, ?, ?, ?, ?), ", n-1) + "(?, ?, ?, ?, ?)"
		args := make([]any, 0, 5*n)
		for _, e := range buf[:n] {
			args = append(args, e.Time.UnixMilli(), e.Method, e.Path, e.Status, e.Ms)
		}
		if _, err := l.W.ExecContext(ctx, q, args...); err != nil {
			return err
		}
		buf = buf[n:]
	}
	return nil
}

// Prune deletes rows older than Retention.
func (l *Log) Prune(ctx context.Context) error {
	_, err := l.W.ExecContext(ctx, `DELETE FROM request_log WHERE created < ?`, time.Now().Add(-Retention).UnixMilli())
	return err
}

// Run flushes every few seconds and prunes every hour until ctx ends, then flushes once more.
func (l *Log) Run(ctx context.Context) {
	flush, prune := time.NewTicker(5*time.Second), time.NewTicker(time.Hour)
	defer flush.Stop()
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := l.Flush(context.Background()); err != nil {
				log.Println("request log:", err)
			}
			return
		case <-flush.C:
			if err := l.Flush(ctx); err != nil {
				log.Println("request log:", err)
			}
		case <-prune.C:
			if err := l.Prune(ctx); err != nil {
				log.Println("request log:", err)
			}
		}
	}
}

// Since returns the entries logged at or after t, oldest first, flushing the buffer first so
// the answer includes the latest requests. r reads.
func (l *Log) Since(ctx context.Context, r DBTX, t time.Time) ([]Entry, error) {
	if err := l.Flush(ctx); err != nil {
		return nil, err
	}
	rows, err := r.QueryContext(ctx, `SELECT created, method, path, status, ms FROM request_log WHERE created >= ? ORDER BY created`, t.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		var ms int64
		if err := rows.Scan(&ms, &e.Method, &e.Path, &e.Status, &e.Ms); err != nil {
			return nil, err
		}
		e.Time = time.UnixMilli(ms)
		out = append(out, e)
	}
	return out, rows.Err()
}
