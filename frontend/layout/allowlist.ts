import type { CheckName } from './checks'

export interface Allowed {
  /** A route's `name` in `routes.ts`. */
  route: string
  widths: readonly number[]
  check: CheckName
  /** The page or component whose change fixes it. */
  owner: string
  /** What is wrong, in one line. */
  why: string
}

/**
 * Violations the pages carry today. Each entry passes every violation of its
 * check on its route at its widths, and an entry that matches nothing fails
 * the run, so a fixed page drops its entries in the same change.
 */
export const ALLOWED: readonly Allowed[] = []
