import type { QueryClient } from '@tanstack/react-query'

import { spendingPlanKeys } from '@/lib/spendingPlan/api'

export const seriesKeys = {
  all: ['series'] as const,
  list: (tab: string, search: string) => ['series', tab, search] as const,
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
