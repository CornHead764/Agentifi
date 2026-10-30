import { selectionForToken, type DateSelection } from '@/lib/dateRanges'
import { parseIsoDay } from '@/lib/format'
import { isUuid } from '@/lib/uuid'

import { EMPTY_DRAFT, type FilterDraft, type IdFacet } from './filter'

/**
 * The state the register opens in, from the URL. The parameters are a contract
 * with the dashboard's tiles and `links.ts`. Read once as initial state, so a
 * chip the reader clears stays cleared.
 */
export interface OpeningFilter {
  panel: FilterDraft
  /** Null is not "all time": it lets the space's default range apply. */
  dates: DateSelection | null
}

export function openingFilter(params: URLSearchParams, today: Date = new Date()): OpeningFilter {
  return {
    panel: {
      ...EMPTY_DRAFT,
      // Only `0` narrows, so a typo is not a filter.
      isReviewed: params.get('isReviewed') === '0' ? false : null,
      categories: openingCategories(params),
      uncategorized: params.get('uncategorized') === '1',
      categoryUndetermined: params.get('undetermined') === '1',
      isBillOrSubscription: params.get('isBill') === '1' ? true : null,
      missingReceipt: params.get('missingReceipt') === '1' ? true : null,
    },
    dates: openingDates(params, today),
  }
}

/** `?category=<id>`, repeatable. Only well-formed ids are kept. */
function openingCategories(params: URLSearchParams): IdFacet {
  const ids = params.getAll('category').filter((id) => isUuid(id))
  return ids.length === 0 ? EMPTY_DRAFT.categories : { ids, negated: false }
}

/** `?datePreset=this-month`, or `?from=&to=`. Unparseable values are dropped. */
function openingDates(params: URLSearchParams, today: Date): DateSelection | null {
  const token = params.get('datePreset')
  if (token !== null) {
    const selection = selectionForToken(token, today)
    if (selection !== null) return selection
  }

  const from = params.get('from')
  const to = params.get('to')
  const start = from !== null && parseIsoDay(from) !== null ? from : null
  const end = to !== null && parseIsoDay(to) !== null ? to : null
  if (start === null && end === null) return null
  return { range: { from: start, to: end }, preset: null }
}
