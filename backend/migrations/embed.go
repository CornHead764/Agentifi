// Package migrations carries the schema inside the binary.
//
// `go:embed` cannot reach outside its own package directory, so the embed
// declaration lives here beside the SQL rather than in internal/store. The
// point is that `agentifi migrate` needs no files on disk: one binary and a
// database file is the whole deployment.
//
// The SQLite schema in sqlite/ is the one the server runs. The Postgres
// migrations beside this file are the schema of a database written by an
// earlier, Postgres-backed release, which `agentifi import-postgres` reads
// from; they are never applied.
package migrations

import (
	"embed"
	"io/fs"
)

//go:embed sqlite/*.sql
var sqliteFS embed.FS

// FS is the SQLite schema, one goose migration per file.
var FS = mustSub(sqliteFS, "sqlite")

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
