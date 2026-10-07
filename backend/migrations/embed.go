// Package migrations carries the schema inside the binary.
//
// `go:embed` cannot reach outside its own package directory, so the embed
// declaration lives here beside the SQL rather than in internal/store. The
// point is that `agentifi migrate` needs no files on disk: one binary and a
// Postgres URL is the whole deployment.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
