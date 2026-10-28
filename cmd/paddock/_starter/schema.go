package main

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
	"github.com/rbxnk/paddock/account"
	"github.com/rbxnk/paddock/auth"
)

// The migrations, run by serve before it listens. Add the app's own as 2, 3, … (Go, or SQL
// files embedded with goose.WithFS).
func migrations(db *sql.DB) (*goose.Provider, error) {
	return goose.NewProvider(goose.DialectSQLite3, db, nil,
		goose.WithDisableGlobalRegistry(true),
		goose.WithGoMigrations(
			goose.NewGoMigration(1, &goose.GoFunc{RunTx: execTx(account.Schema + auth.Schema + auth.MethodsSchema)},
				&goose.GoFunc{RunTx: execTx(`DROP TABLE auth_methods; DROP TABLE auth_links; DROP TABLE sessions; DROP TABLE users;`)}),
		),
	)
}

func execTx(stmts string) func(context.Context, *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, stmts)
		return err
	}
}

func migrateUp(ctx context.Context, db *sql.DB) error {
	p, err := migrations(db)
	if err != nil {
		return err
	}
	_, err = p.Up(ctx)
	return err
}

func migrateDown(ctx context.Context, db *sql.DB) error {
	p, err := migrations(db)
	if err != nil {
		return err
	}
	_, err = p.Down(ctx)
	return err
}
