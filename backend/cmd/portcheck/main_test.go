package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiffCommand(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	before := `{"format_version":1,"households":{"home":{"GET /transactions?account_id=bank":{"status":200,"body":{"running_balance":"10.00"}}}}}`
	after := strings.Replace(before, "10.00", "11.00", 1)
	for path, content := range map[string]string{a: before, b: after} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"diff", a, a}, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "0 differences\n" {
		t.Fatalf("identical diff: %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), []string{"diff", a, b}, &out); !errors.Is(err, different) {
		t.Fatalf("changed diff: %v", err)
	}
	if !strings.Contains(out.String(), "/households/home/GET ~1transactions?account_id=bank/body/running_balance") || !strings.Contains(out.String(), "1 differences") {
		t.Fatalf("changed output: %s", &out)
	}
}
