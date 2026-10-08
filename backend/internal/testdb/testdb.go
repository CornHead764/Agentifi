// Package testdb is the database bootstrap every database-backed test binary
// shares: a private SQLite file, the connection to it, and the removal
// afterwards. Not a _test.go file because Go does not share test code across
// packages.
//
// A file per process, in its own temporary directory, so several packages and
// concurrent runs never share tables.
//
// The caller connects through the open callback because internal/store is a
// caller and cannot import a package that imports it.
package testdb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Schema is a private database file, live until Drop.
type Schema struct {
	// Name is exported for callers deriving per-process resources from it.
	Name string
	// Path is the database file, for tests that open a second connection.
	Path string
	dir  string
}

// Name is the database this process uses for a given prefix. Deterministic, so
// a caller can name a temporary directory to match before Start has run.
func Name(prefix string) string {
	return fmt.Sprintf("agentifi_%s_test_%d", prefix, os.Getpid())
}

// Start creates the file and hands its path to open, which connects and
// migrates. It returns a live schema, or nil and the reason it could not.
func Start(ctx context.Context, prefix string, open func(context.Context, string) error) (*Schema, string) {
	name := Name(prefix)
	dir, err := os.MkdirTemp("", name+"_")
	if err != nil {
		return nil, fmt.Sprintf("creating a directory for %s: %v", name, err)
	}
	schema := &Schema{Name: name, Path: filepath.Join(dir, name+".db"), dir: dir}
	if err := open(ctx, schema.Path); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Sprintf("opening %s: %v", schema.Path, err)
	}
	return schema, ""
}

// Drop removes the file. Failures are silent, since the suite's result is
// already decided.
func (s *Schema) Drop() { _ = os.RemoveAll(s.dir) }
