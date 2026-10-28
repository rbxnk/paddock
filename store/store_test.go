package store_test

import (
	"strings"
	"testing"
	"time"

	"github.com/rbxnk/paddock/store"
)

// Times are stored in TimeLayout and read back, and ids are 15 lowercase letters and digits.
func TestTimesAndIds(t *testing.T) {
	at := time.Date(2026, 10, 26, 17, 55, 45, 688_000_000, time.FixedZone("x", 3600))
	if s := store.FormatTime(at); s != "2026-10-26 16:55:45.688Z" {
		t.Fatalf("formatted %q", s)
	}
	for _, s := range []string{"2026-10-26 16:55:45.688Z", "2026-10-26T16:55:45.688Z"} {
		if got, err := store.ParseTime(s); err != nil || !got.Equal(at) {
			t.Errorf("parse %q: %v %v", s, got, err)
		}
	}
	if got, err := store.ParseTime(""); err != nil || !got.IsZero() || store.FormatTime(got) != "" {
		t.Errorf("empty time: %v %v", got, err)
	}
	if id := store.NewId(); len(id) != 15 || strings.Trim(id, "abcdefghijklmnopqrstuvwxyz0123456789") != "" {
		t.Errorf("id %q", id)
	}
}
