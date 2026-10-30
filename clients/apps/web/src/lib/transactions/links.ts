import { ALL_ACCOUNTS_ID } from '@/components/shell/accountTree'

import { parseIsoDate, toIsoDate } from '@/lib/format'
import type { RegisterTab, Uuid } from './types'

/**
 * The review queue: unreviewed rows across every account. Names All Accounts
 * explicitly, since the space-wide "N waiting" counts link here and the
 * register otherwise opens on the last-picked account.
 */
export function reviewQueueLink(
  narrowing: { uncategorized?: boolean; isBill?: boolean } = {},
): string {
  const params = new URLSearchParams({ displayNode: ALL_ACCOUNTS_ID, isReviewed: '0' })
  if (narrowing.uncategorized) params.set('uncategorized', '1')
  if (narrowing.isBill) params.set('isBill', '1')
  return `/transactions?${params.toString()}`
}

/**
 * Every row missing a receipt, on every account and over all time, which is
 * the window the dashboard's count is taken over (trap 5).
 */
export function missingReceiptsLink(): string {
  const params = new URLSearchParams({
    displayNode: ALL_ACCOUNTS_ID,
    missingReceipt: '1',
    datePreset: 'all-time',
  })
  return `/transactions?${params.toString()}`
}

/**
 * The register on the row's account, two weeks either side, with the row
 * marked. `open` also opens its detail dialog; the window is still built so
 * closing it leaves the row on screen.
 */
export function registerLinkFor(
  row: { id: Uuid; account_id: Uuid; date: string },
  options: { open?: boolean } = {},
): string {
  const on = parseIsoDate(row.date)
  const shift = (days: number) =>
    toIsoDate(new Date(on.getFullYear(), on.getMonth(), on.getDate() + days))
  const params = new URLSearchParams({
    displayNode: row.account_id,
    from: shift(-14),
    to: shift(14),
    highlight: row.id,
  })
  if (options.open) params.set('edit', row.id)
  return `/transactions?${params.toString()}`
}

/**
 * The register over one window, every account, optionally narrowed to one
 * category, read back by `openingFilter` into the shared `FilterDraft` (ground
 * rule 3). The window and all-accounts scope ride along so the total matches
 * the figure clicked (trap 5). An empty category id means uncategorized. The
 * Spending and Income tabs read effective dates, as a report does (trap 4).
 */
export function registerLinkForWindow(
  window: { from: string | null; to: string | null },
  narrowing: { categoryId?: string; tab?: RegisterTab } = {},
): string {
  const params = new URLSearchParams({ displayNode: ALL_ACCOUNTS_ID })
  if (narrowing.tab !== undefined && narrowing.tab !== 'all') params.set('tab', narrowing.tab)
  if (narrowing.categoryId === '') params.set('uncategorized', '1')
  else if (narrowing.categoryId !== undefined) params.set('category', narrowing.categoryId)
  if (window.from !== null) params.set('from', window.from)
  if (window.to !== null) params.set('to', window.to)
  return `/transactions?${params.toString()}`
}

export function registerLinkForAccount(accountId: Uuid): string {
  return `/transactions?displayNode=${accountId}`
}
