// __NAME__: an app on the kit (github.com/rbxnk/paddock), made by `kit new`.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/a-h/templ"
	"github.com/rbxnk/paddock/account"
	"github.com/rbxnk/paddock/assets"
	"github.com/rbxnk/paddock/mail"
	"github.com/rbxnk/paddock/signin"
	"github.com/rbxnk/paddock/store"
	"github.com/rbxnk/paddock/web"
)

const appName = "__NAME__"

// event is a request to the app; add the app's own per-request fields here.
type event struct{ *web.Event }

type app struct {
	db      *store.Store
	users   account.Store
	signins *signin.Signin
	head    templ.Component
}

func main() {
	log.SetFlags(0)
	dbPath := flag.String("db", "data/"+appName+".db", "the database file")
	dev := flag.Bool("dev", false, "dev mode: emails to the log, /login_as, plain-http cookies")
	addr := flag.String("http", "127.0.0.1:8090", "the address serve listens on")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [flags] serve | migrate up|down | superuser EMAIL\n", appName)
		flag.PrintDefaults()
	}
	flag.Parse()
	if err := run(flag.Args(), *dbPath, *dev, *addr); err != nil {
		log.Fatal(err)
	}
}

func run(args []string, dbPath string, dev bool, addr string) error {
	if len(args) == 0 {
		flag.Usage()
		os.Exit(2)
	}
	a, err := newApp(dbPath, dev)
	if err != nil {
		return err
	}
	defer a.db.Close()
	ctx := context.Background()
	switch {
	case args[0] == "serve" && len(args) == 1:
		return a.serve(ctx, addr)
	case args[0] == "migrate" && len(args) == 2 && args[1] == "up":
		return migrateUp(ctx, a.db.W)
	case args[0] == "migrate" && len(args) == 2 && args[1] == "down":
		return migrateDown(ctx, a.db.W)
	case args[0] == "superuser" && len(args) == 2:
		return a.superuser(ctx, strings.TrimSpace(args[1]))
	}
	flag.Usage()
	os.Exit(2)
	return nil
}

func newApp(dbPath string, dev bool) (*app, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	db, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	a := &app{db: db, users: account.Store{DB: db}}
	appURL := os.Getenv("APP_URL")
	if appURL == "" {
		appURL = "http://localhost:8090"
	}
	a.signins = &signin.Signin{
		DB:     db,
		Users:  a.users,
		Cookie: appName + "_session",
		Dev:    dev,
		Mailer: func() (*mail.Mailer, error) { return mail.FromEnv(dev) },
		App:    func() (string, string) { return appName, appURL },
		Render: func(e *web.Event, status int, content templ.Component, crumb string) error {
			return a.render(e, status, content)
		},
	}
	css, err := web.AssetURL(os.DirFS("static"), "/static/", "dist/app.css")
	if err != nil {
		return nil, err
	}
	htmx, err := assets.URL("htmx.min.js")
	if err != nil {
		return nil, err
	}
	hxLive, err := assets.URL("hx-live.min.js")
	if err != nil {
		return nil, err
	}
	a.head = Head(css, htmx, hxLive)
	return a, nil
}

func (a *app) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static", web.Static(os.DirFS("static"))))
	mux.Handle("GET "+assets.Prefix, assets.Handler())
	r := &web.Router[*event]{
		Mux: mux,
		MW: []web.Middleware[*event]{web.RecoverPanic[*event], web.LimitBody[*event], logErrors,
			web.HTMXErrors[*event], signin.Session[*event](a.signins)},
		New: func(e *web.Event) *event { return &event{e} },
	}
	signin.Mount(r, a.signins)
	r.GET("/{$}", a.home)
	return mux
}

// logErrors prints errors that aren't the handlers' own (web.Error): bugs and failures.
func logErrors(e *event, next web.Handler[*event]) error {
	err := next(e)
	if _, ok := errors.AsType[*web.Error](err); err != nil && (!ok || web.AsError(err).Status >= 500) {
		log.Println(e.Request.Method, e.Request.URL.Path, err)
	}
	return err
}

func (a *app) home(e *event) error {
	return a.render(e.Event, http.StatusOK, Home(e.Auth))
}

func (a *app) render(e *web.Event, status int, content templ.Component) error {
	e.Response.Header().Set("Cache-Control", "no-store") // pages change via htmx swaps
	return e.Render(status, Page(a.head, e.Auth, content))
}

// serve migrates the database, then serves the app on addr until SIGINT or SIGTERM.
func (a *app) serve(ctx context.Context, addr string) error {
	if err := migrateUp(ctx, a.db.W); err != nil {
		return err
	}
	if !a.signins.Dev {
		web.ClientIPHeader = "Fly-Client-IP" // set by Fly's proxy, which every public request comes through
	}
	srv := &http.Server{Addr: addr, Handler: a.routes(), ReadHeaderTimeout: 30 * time.Second, ReadTimeout: 10 * time.Minute}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	log.Println("Server started at http://" + addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// superuser makes email a superuser, creating the account if it's new, and emails a
// set-password link (to the log with --dev).
func (a *app) superuser(ctx context.Context, email string) error {
	if err := migrateUp(ctx, a.db.W); err != nil {
		return err
	}
	u, err := a.users.ByEmail(ctx, email)
	if errors.Is(err, account.ErrNotFound) {
		u = account.User{Email: email, Superuser: true}
		err = a.users.Create(ctx, &u, "")
	} else if err == nil && !u.Superuser {
		u.Superuser = true
		err = a.users.Update(ctx, u)
	}
	if err != nil {
		return err
	}
	return a.signins.MailLink(u, "reset")
}
