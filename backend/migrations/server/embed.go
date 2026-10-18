// Package server carries the server database migrations.
package server

import "embed"

//go:embed *.sql
var FS embed.FS
