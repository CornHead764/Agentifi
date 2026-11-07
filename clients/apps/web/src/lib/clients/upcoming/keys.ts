import type { QueryClient } from '@tanstack/react-query'

import { spendingPlanKeys } from '@/lib/spendingPlan/api'
import { TRANSACTIONS_KEY } from '@/lib/transactions/cache'

export const seriesKeys = {
  all: ['series'] as const,
  list: (tab: string, search: string) => ['series', tab, search] as const,
  one: (id: string | null) => ['series', 'one', id] as const,
  history: (id: string | null, toMonth: string, months: number) =>
    ['series', 'history', id, toMonth, months] as const,
  everything: (search: string) => ['series', 'all', search] as const,
  suggested: ['series', 'suggested'] as const,
  suggestions: (dismissed: boolean) =>
    ['series', 'suggested', dismissed ? 'dismissed' : 'open'] as const,
  suggestedFor: (transactionId: string) => ['series', 'suggested', 'for', transactionId] as const,
  refunds: ['series', 'refunds'] as const,
}

export const occurrenceKeys = {
  all: ['occurrences'] as const,
  range: (from: string, to: string, includeFulfilled: boolean) =>
    ['occurrences', from, to, includeFulfilled] as const,
  suggestedPayments: (seriesId: string, dueOn: string) =>
    ['occurrences', 'suggested-payments', seriesId, dueOn] as const,
}

export const cashFlowKeys = {
  all: ['cash-flow'] as const,
  range: (from: string, to: string, accounts: string, threshold: string) =>
    ['cash-flow', from, to, accounts, threshold] as const,
}

/**
 * Any series or occurrence write moves all four: series, occurrences, cash
 * flow and the spending plan's bills bucket.
 */
export function invalidateOccurrenceWrites(client: QueryClient): void {
  for (const queryKey of [
    seriesKeys.all,
    occurrenceKeys.all,
    cashFlowKeys.all,
    spendingPlanKeys.all,
  ]) {
    void client.invalidateQueries({ queryKey })
  }
}

/**
 * Marking a slot paid or taking the mark back also writes or deletes a
 * transaction on an account no bank feeds, so the register is asked again too.
 */
export function invalidateMarkWrites(client: QueryClient): void {
  invalidateOccurrenceWrites(client)
  void client.invalidateQueries({ queryKey: TRANSACTIONS_KEY })
}
