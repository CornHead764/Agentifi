// Package testdb is the Postgres bootstrap every database-backed test binary
// shares: a private schema, the connection to it, and the drop afterwards. Not
// a _test.go file because Go does not share test code across packages.
//
// A schema per process: several packages and concurrent runs test against
// one development database at once, and a fixed name lets one run drop
// another's tables. The pid in the name makes teardown unconditional.
//
// An unreachable database skips on a workstation but is fatal in CI, where a
// skipped test would read exactly like a passing one.
//
// The caller connects through the open callback because internal/store is a
// caller and cannot import a package that imports it.
package testdb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// dropTimeout bounds the teardown. TestMain exits through os.Exit, so a
// teardown that hangs hangs the whole binary with no output.
const dropTimeout = 30 * time.Second

// Schema is a private schema, live until Drop.
type Schema struct {
	// Name is exported for tests that query the schema by name and callers
	// deriving per-process resources from it.
	Name string
	url  string
}

// Name is the schema this process uses for a given prefix. Deterministic, so a
// caller can name a temporary directory to match before Start has run.
func Name(prefix string) string {
	return fmt.Sprintf("agentifi_%s_test_%d", prefix, os.Getpid())
}

// Start creates the schema and hands it to open, which connects and migrates.
// It returns a live schema, or nil and a skip reason — except under CI, where
// it exits the process instead.
func Start(ctx context.Context, prefix string, open func(context.Context, *pgxpool.Config) error) (*Schema, string) {
	schema := &Schema{Name: Name(prefix), url: databaseURL()}
	if err := schema.start(ctx, open); err != nil {
		if os.Getenv("CI") != "" {
			fmt.Fprintf(os.Stderr, "CI requires a database: %v\n", err)
			os.Exit(1)
		}
		return nil, err.Error()
	}
	return schema, ""
}

func (s *Schema) start(ctx context.Context, open func(context.Context, *pgxpool.Config) error) error {
	// The schema must exist before a pool with search_path set to it connects:
	// goose creates its tables unqualified.
	bootstrap, err := pgxpool.New(ctx, s.url)
	if err != nil {
		return fmt.Errorf("postgres is unreachable at %s: %w", s.url, err)
	}
	if err := bootstrap.Ping(ctx); err != nil {
		bootstrap.Close()
		return fmt.Errorf("postgres is unreachable at %s: %w", s.url, err)
	}
	_, err = bootstrap.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS `+s.Name)
	bootstrap.Close()
	if err != nil {
		return fmt.Errorf("creating schema %s: %w", s.Name, err)
	}

	cfg, err := pgxpool.ParseConfig(s.url)
	if err != nil {
		return fmt.Errorf("parsing database url: %w", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = s.Name
	if err := open(ctx, cfg); err != nil {
		return fmt.Errorf("opening %s: %w", s.Name, err)
	}
	return nil
}

// Drop removes the schema and everything in it. Failures are silent, since the
// suite's result is already decided.
func (s *Schema) Drop() {
	ctx, cancel := context.WithTimeout(context.Background(), dropTimeout)
	defer cancel()
	cleanup, err := pgxpool.New(ctx, s.url)
	if err != nil {
		return
	}
	defer cleanup.Close()
	_, _ = cleanup.Exec(ctx, `DROP SCHEMA IF EXISTS `+s.Name+` CASCADE`)
}

// URL is where the suites connect, for tests that open a second connection.
func URL() string { return databaseURL() }

// databaseURL prefers TEST_DATABASE_URL, else the repository's development
// server on a unix socket under .dev-postgres (scripts/dev-postgres.py), found
// by walking up because callers sit at different depths.
func databaseURL() string {
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		return url
	}
	// scripts/dev-postgres.py creates this database; change them together.
	return "postgres://postgres@/agentifi_test?host=" + socketDir()
}

func socketDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	// .git marks the root even before the server has ever been started, so an
	// unreachable database still names the path it looked for.
	root := ""
	for {
		if _, err := os.Stat(filepath.Join(dir, ".dev-postgres")); err == nil {
			return filepath.Join(dir, ".dev-postgres")
		}
		if root == "" {
			if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
				root = dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if root == "" {
		return ""
	}
	return filepath.Join(root, ".dev-postgres")
}
