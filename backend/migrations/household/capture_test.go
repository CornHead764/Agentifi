package household

import (
	"strings"
	"testing"
)

func TestCommittedChangeCaptureIsCurrent(t *testing.T) {
	data, err := FS.ReadFile("00001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(data), "-- BEGIN GENERATED CHANGE CAPTURE\n")
	if !ok {
		t.Fatal("missing generated section")
	}
	committed, _, ok := strings.Cut(rest, "-- END GENERATED CHANGE CAPTURE\n")
	if !ok {
		t.Fatal("missing generated section end")
	}
	if committed != ChangeCaptureSQL() {
		t.Fatal("change capture SQL is stale: run go generate ./migrations/household")
	}
}
