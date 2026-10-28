package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// made is generated or built in an app (go mod tidy rewrites go.mod), not copied as is.
func made(path string) bool {
	return strings.HasSuffix(path, "_templ.go") || path == "go.sum" || path == "go.mod" || strings.HasPrefix(path, "static/") || strings.HasPrefix(path, "data/")
}

// TestStarterMatchesHello: apps/hello is `paddock new hello` as committed, so CI building and
// testing it checks the starter.
func TestStarterMatchesHello(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hello")
	if err := create("hello", dir, "v0.0.0", "replace "+module+" => ../../kit\n"); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("..", "..", "..", "apps", "hello")
	seen := map[string]bool{}
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		seen[rel] = true
		if made(filepath.ToSlash(rel)) {
			return nil
		}
		got, _ := os.ReadFile(path)
		w, err := os.ReadFile(filepath.Join(want, rel))
		if err != nil || string(got) != string(w) {
			t.Errorf("apps/hello/%s differs from the starter (%v)", rel, err)
		}
		return nil
	})
	filepath.WalkDir(want, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Type()&fs.ModeSymlink != 0 {
			return err
		}
		rel, _ := filepath.Rel(want, path)
		if !seen[rel] && !made(filepath.ToSlash(rel)) && !strings.HasPrefix(rel, ".bin") {
			t.Errorf("apps/hello/%s isn't in the starter", rel)
		}
		return nil
	})
}

// TestGoMod: a release requires its own version with no replace; the kit's repository adds one.
func TestGoMod(t *testing.T) {
	for _, c := range []struct{ version, replace, want, wantNot string }{
		{"v0.1.0", "", "github.com/rbxnk/paddock v0.1.0\n", "replace"},
		{"v0.0.0", "replace github.com/rbxnk/paddock => ../../kit\n", "replace github.com/rbxnk/paddock => ../../kit\n", "__"},
	} {
		dir := filepath.Join(t.TempDir(), "app")
		if err := create("app", dir, c.version, c.replace); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
		if s := string(b); !strings.Contains(s, c.want) || strings.Contains(s, c.wantNot) {
			t.Errorf("go.mod:\n%s", s)
		}
	}
}

func TestCreateRefuses(t *testing.T) {
	full := t.TempDir()
	os.WriteFile(filepath.Join(full, "x"), nil, 0o644)
	if err := create("Bad Name", filepath.Join(t.TempDir(), "a"), "v0.1.0", ""); err == nil {
		t.Error("bad name: no error")
	}
	if err := create("full", full, "v0.1.0", ""); err == nil {
		t.Error("non-empty dir: no error")
	}
}
