import { ASSISTANT } from './assistant'
import { AUTH } from './auth'
import { CORE } from './core'
import { INVESTING } from './investing'
import { LEDGER } from './ledger'
import { MERCHANTS } from './merchants'
import { PLANNING } from './planning'
import { SHELL } from './shell'

/**
 * `METHOD /path` to a response body, an `Answered`, or a function of the
 * query string, the request's body and the path that returns either. `:name`
 * matches any one segment; an exact key wins over a pattern.
 */
export type Fixture = unknown

export const FIXTURES: Record<string, Fixture> = {
  ...AUTH,
  ...CORE,
  ...SHELL,
  ...LEDGER,
  ...PLANNING,
  ...INVESTING,
  ...MERCHANTS,
  ...ASSISTANT,
}
