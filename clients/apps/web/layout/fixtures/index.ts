import { AUTH } from './auth'
import { CORE } from './core'
import { INVESTING } from './investing'
import { LEDGER } from './ledger'
import { PLANNING } from './planning'
import { SETTINGS } from './settings'
import { SHELL } from './shell'

/**
 * `METHOD /path` to a response body, a `{ status, body }` pair, or a function
 * of the query string and the request's JSON body that returns either. `:name` matches any one segment;
 * an exact key wins over a pattern.
 */
export type Fixture = unknown

export const FIXTURES: Record<string, Fixture> = {
  ...AUTH,
  ...CORE,
  ...SHELL,
  ...LEDGER,
  ...PLANNING,
  ...INVESTING,
  ...SETTINGS,
}
