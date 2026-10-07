package importer

import (
	"testing"

	"github.com/CornHead764/agentifi/backend/internal/storetest"
)

// The writing tests run against a real Postgres, because numeric precision,
// insert order against foreign keys and uuid[] round trips are properties of
// the database.

func TestMain(m *testing.M) { storetest.Main(m, "import") }

var db = storetest.DB
