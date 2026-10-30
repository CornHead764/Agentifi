package api

import "github.com/CornHead764/agentifi/backend/internal/service"

// NewCategoryEvaluator uses the same context, vote and tools as a live check.
// Its environment must point at a disposable, time-restricted evaluation store.
func NewCategoryEvaluator(env *Env) *service.Automations {
	a := service.NewAutomations(env.DB, automationHost{env: env})
	a.Now = env.Now
	return a
}
