package nullid

import (
	"testing"

	"github.com/google/uuid"
)

func TestZeroUUIDWritesNull(t *testing.T) {
	if v, err := From(uuid.Nil).Value(); v != nil || err != nil {
		t.Errorf("From(uuid.Nil) writes %v, %v; want NULL", v, err)
	}
	if v, err := (ID{}).Value(); v != nil || err != nil {
		t.Errorf("the zero ID writes %v, %v; want NULL", v, err)
	}
	id := uuid.MustParse("6f1d2c3b-4a59-4e68-8f7a-0b1c2d3e4f50")
	if v, err := From(id).Value(); v != id.String() || err != nil {
		t.Errorf("From(%s) writes %v, %v", id, v, err)
	}
}

func TestNullReadsZeroUUID(t *testing.T) {
	id := uuid.MustParse("6f1d2c3b-4a59-4e68-8f7a-0b1c2d3e4f50")
	n := From(id)
	if err := n.Scan(nil); err != nil || n.UUID() != uuid.Nil {
		t.Errorf("NULL reads as %s, %v", n.UUID(), err)
	}
	if err := n.Scan(id.String()); err != nil || n.UUID() != id {
		t.Errorf("%s reads as %s, %v", id, n.UUID(), err)
	}
	if err := n.Scan("not-a-uuid"); err == nil {
		t.Error("a malformed id scanned")
	}
}
