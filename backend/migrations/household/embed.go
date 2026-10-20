package household

import "embed"

// FS contains the household database migrations.
//
//go:embed *.sql
var FS embed.FS

//go:generate go run ./generate
