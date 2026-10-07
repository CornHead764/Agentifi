package store_test

import (
	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Compile-time check that the store satisfies auth.RecoveryCodeStore. It lives
// in an external test package because internal/auth imports internal/store.
// auth.PasskeyStore needs an adapter and is asserted beside it in
// internal/api/credentialstore.go.
var _ auth.RecoveryCodeStore = (*store.Store)(nil)
