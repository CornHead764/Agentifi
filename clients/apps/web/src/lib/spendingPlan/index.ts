/**
 * The spending plan on the client. Shapes mirror
 * `backend/internal/domain/spendingplan.go` and `envelopes.go`; the engine
 * computes every bucket and headline figure, and the screen draws them as sent.
 */

export * from './types'
export * from './derive'
export * from './months'
export * from './api'
