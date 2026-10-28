# paddock

A small base for server-rendered Go apps: accounts and sign-in (password or emailed link,
sessions, rate limits), SQLite with Litestream, mail, uploads to an S3-style bucket, a
`net/http` router with htmx-aware errors, and daisyUI sign-in pages. htmx 4, Tailwind 4 and
daisyUI, no Node.

Start an app:

```sh
go run github.com/rbxnk/paddock/cmd/paddock@latest new myapp
cd myapp && make run
```

`paddock new` does the first build (the templ code, and the CSS with the Tailwind standalone CLI
it installs into `.bin/`), so `go build` and `go test` work right away. `make run` redoes both and
serves the app on http://127.0.0.1:8090 with `--dev` (emails go to the log).
`go run . --dev superuser you@example.com` makes an account and logs its set-password link.

Releases only: each version is one commit and its tag. Versions are v0 while the API changes.
