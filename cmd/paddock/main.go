// Command paddock makes apps on the kit. Anywhere:
//
//	go run github.com/rbxnk/paddock/cmd/paddock@VERSION new NAME [DIR]
//
// writes a working starter app (home page, sign-in, migrations, Dockerfile, Fly and Litestream
// config) into DIR, ./NAME by default, on that release. From the root of the repository the
// kit is developed in (kit/ next to it), `go run ./kit/cmd/paddock new NAME` writes apps/NAME
// on the kit's working copy instead, through a replace line and go.work.
package main

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
)

const module = "github.com/rbxnk/paddock"

//go:embed all:_starter
var starter embed.FS

func main() {
	log.SetFlags(0)
	args := os.Args[1:]
	if len(args) < 2 || len(args) > 3 || args[0] != "new" {
		log.Fatal("usage: paddock new NAME [DIR]")
	}
	name, dir := args[1], args[1]
	dev := devRepo()
	if dev {
		dir = filepath.Join("apps", name)
	}
	if len(args) == 3 {
		dir = args[2]
	}
	version, replace := "v0.0.0", ""
	if dev {
		abs, err := filepath.Abs(dir)
		if err != nil {
			log.Fatal(err)
		}
		kit, err := filepath.Abs("kit")
		if err != nil {
			log.Fatal(err)
		}
		rel, err := filepath.Rel(abs, kit)
		if err != nil {
			log.Fatal(err)
		}
		replace = "replace " + module + " => " + filepath.ToSlash(rel) + "\n"
	} else if bi, ok := debug.ReadBuildInfo(); ok && strings.HasPrefix(bi.Main.Version, "v") {
		version = bi.Main.Version
	} else {
		log.Fatal("run a release: go run " + module + "/cmd/paddock@VERSION new " + name)
	}
	if err := create(name, dir, version, replace); err != nil {
		log.Fatal(err)
	}
	if err := goCmd(dir, "mod", "tidy"); err != nil {
		log.Fatal(err)
	}
	if _, err := os.Stat("go.work"); err == nil && dev {
		if err := goCmd(".", "work", "use", dir); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Printf("made %s in %s; next: cd %s && make run\n", name, dir, dir)
}

// devRepo is whether this is the root of the kit's own repository: kit/ is the module.
func devRepo() bool {
	b, err := os.ReadFile(filepath.Join("kit", "go.mod"))
	return err == nil && strings.HasPrefix(string(b), "module "+module+"\n")
}

func goCmd(dir string, args ...string) error {
	cmd := exec.Command("go", args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

var validName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// create writes the starter into dir, an empty or new directory, requiring the kit at version
// and adding replace (a go.mod line, or "").
func create(name, dir, version, replace string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("name %q: use lowercase letters, digits and dashes", name)
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s isn't empty", dir)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	fill := strings.NewReplacer("__NAME__", name, "__VERSION__", version, "__REPLACE__\n", replace)
	return fs.WalkDir(starter, "_starter", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(path, "_templ.go") {
			return err
		}
		b, err := starter.ReadFile(path)
		if err != nil {
			return err
		}
		out := strings.TrimSuffix(strings.TrimPrefix(path, "_starter/"), ".tmpl")
		if out == "gitignore" || out == "dockerignore" {
			out = "." + out
		}
		out = filepath.Join(dir, filepath.FromSlash(out))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, []byte(fill.Replace(string(b))), 0o644)
	})
}
