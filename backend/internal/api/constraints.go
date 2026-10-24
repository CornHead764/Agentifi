package api

import (
	"errors"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// isUniqueViolation recognizes classified SQLite uniqueness errors.
func isUniqueViolation(err error) bool {
	var constraint *store.ConstraintError
	return errors.As(err, &constraint) && constraint.Code == "23505"
}
