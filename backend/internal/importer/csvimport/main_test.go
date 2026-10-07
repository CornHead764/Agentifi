package csvimport

import (
	"testing"

	"github.com/CornHead764/agentifi/backend/internal/storetest"
)

// The writing tests run against a real Postgres, because idempotency, insert
// order and numeric precision are properties of the database.

func TestMain(m *testing.M) { storetest.Main(m, "csvimport") }

var db = storetest.DB
