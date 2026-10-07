package domain

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// internal/domain may import nothing beyond the standard library and this
// allowlist, so the calculation core stays testable without a database or an
// HTTP server. x/text is here for payee accent folding; the stdlib has no
// Unicode normalizer.
var allowedDomainImports = map[string]bool{
	"github.com/shopspring/decimal":   true,
	"golang.org/x/text/unicode/norm":  true,
	"golang.org/x/text/secure/precis": true,
	"golang.org/x/text/cases":         true,
	"golang.org/x/text/currency":      true,
}

func TestTheCalculationCoreImportsNothingBeyondItsAllowlist(t *testing.T) {
	root := "."
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("reading the domain package: %v", err)
	}

	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			// Test-only imports never ship in the binary.
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(root, name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if isStdlib(path) {
				continue
			}
			if !allowedDomainImports[path] {
				t.Errorf("%s imports %q, which is not on the domain allowlist", name, path)
			}
		}
	}
}

// isStdlib reports whether an import path belongs to the standard library:
// no dot in the first path element means no domain name, means stdlib.
func isStdlib(path string) bool {
	first := path
	if cut := strings.IndexByte(path, '/'); cut >= 0 {
		first = path[:cut]
	}
	return !strings.Contains(first, ".")
}
