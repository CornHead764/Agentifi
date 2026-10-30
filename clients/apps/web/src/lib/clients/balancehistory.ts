/**
 * One account's end-of-day balance by posted date. Both ends of the window are
 * always sent: the server has no default.
 */

import { useQuery } from '@tanstack/react-query'

import { api, type MoneyShape } from '@/lib/api'
import type { Money } from '@/lib/money'

import type { IsoDate } from './entities'

export const balanceHistoryKeys = {
  all: ['account-balance-history'] as const,
  range: (accountId: string, from: IsoDate, to: IsoDate) =>
    ['account-balance-history', accountId, from, to] as const,
}

export interface BalanceHistoryPoint {
  on: IsoDate
  balance: Money
}

export interface AccountBalanceHistory {
  account_id: string
  from: IsoDate
  /** Clamped to the server's today. */
  to: IsoDate
  /** The account header's balance, to check the last point against. */
  balance: Money
  points: BalanceHistoryPoint[]
}

const BALANCE_HISTORY_SHAPE: MoneyShape<AccountBalanceHistory> = {
  balance: 'money',
  points: { balance: 'money' },
}

export function useAccountBalanceHistory(
  accountId: string,
  from: IsoDate,
  to: IsoDate,
  enabled = true,
) {
  return useQuery({
    queryKey: balanceHistoryKeys.range(accountId, from, to),
    enabled,
    queryFn: ({ signal }) =>
      api.get<AccountBalanceHistory>(
        `/account-balance-history?${new URLSearchParams({ account_id: accountId, from, to })}`,
        BALANCE_HISTORY_SHAPE,
        signal,
      ),
  })
}
