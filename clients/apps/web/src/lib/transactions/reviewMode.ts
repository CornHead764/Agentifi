/** What the register's review icon does with a click. */

import type { Transaction } from './types'

export type ReviewIconAction = 'open' | 'review' | 'unreview'

/**
 * Review mode makes the icon tick a row in place. A row with a suggestion
 * waiting still opens, since ticking it would throw the proposal away unseen;
 * the suggestion is approved or changed first, which also reviews the row.
 */
export function reviewIconAction(txn: Transaction, reviewMode: boolean): ReviewIconAction {
  if (txn.is_reviewed) return 'unreview'
  if (reviewMode && !txn.suggestion) return 'review'
  return 'open'
}
