package pgconv

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func TestNullDateAndUUIDRoundTrip(t *testing.T) {
	if got := ReadNullDate(NullDate(domain.Date{})); !got.IsZero() {
		t.Errorf("a zero date came back as %v", got)
	}
	day := domain.NewDate(2026, time.March, 14)
	if got := ReadNullDate(NullDate(day)); got != day {
		t.Errorf("a date came back as %v, want %v", got, day)
	}
	if NullUUID(uuid.Nil) != nil {
		t.Error("uuid.Nil was written as a value, not NULL")
	}
	if got := ReadNullUUID(nil); got != uuid.Nil {
		t.Errorf("NULL read as %v", got)
	}
	id := uuid.New()
	if got := ReadNullUUID(NullUUID(id)); got != id {
		t.Errorf("an id came back as %v, want %v", got, id)
	}
}
