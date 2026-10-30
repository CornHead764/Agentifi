/**
 * Possible duplicates: two rows from different sources that look like one
 * charge, such as a Simplifi import and the SimpleFIN sync after it. Nothing
 * is removed until a person says so; a "distinct" answer is remembered by the
 * server and the pair is not asked about again.
 */

import { useQuery } from '@tanstack/react-query'

import { api, type Money, type MoneyShape } from '@/lib/api'
import { useInvalidatingMutation, type Invalidates } from '@/lib/queryClient'
import { ACCOUNTS_KEY, TRANSACTIONS_KEY } from '@/lib/transactions/cache'
import type { IsoDate, TransactionSource, Uuid } from '@/lib/transactions/types'

export interface DuplicateRow {
  id: Uuid
  /** The posted day. */
  date: IsoDate
  amount: Money
  currency: string
  payee: string
  /** The bank's own wording; the two copies usually read differently. */
  statement_name: string
  category_name: string | null
  notes: string | null
  source: TransactionSource
  is_pending: boolean
  /** Retiring this row releases the transfer it is half of. */
  is_transfer_leg: boolean
}

export interface DuplicatePair {
  id: Uuid
  account_id: Uuid
  account_name: string
  days_apart: number
  suggested_keep_id: Uuid
  first: DuplicateRow
  second: DuplicateRow
}

export interface DuplicateList {
  count: number
  pairs: DuplicatePair[]
}

export interface DuplicateScan {
  found: number
  open: number
}

export interface DuplicateDecision {
  id: Uuid
  verdict: 'duplicate' | 'distinct'
  kept_id: Uuid | null
  retired_id: Uuid | null
}

const ROW_SHAPE: MoneyShape<DuplicateRow> = { amount: 'money' }
const LIST_SHAPE: MoneyShape<DuplicateList> = {
  pairs: { first: ROW_SHAPE, second: ROW_SHAPE },
}

export const DUPLICATES_KEY = ['duplicates'] as const

export function useDuplicates() {
  return useQuery({
    queryKey: DUPLICATES_KEY,
    queryFn: ({ signal }) =>
      api.get<DuplicateList>('/transaction-duplicates', LIST_SHAPE, signal),
  })
}

/** A retired copy changes the register, the balances and the transfers. */
const DUPLICATE_WRITE: Invalidates = [DUPLICATES_KEY, TRANSACTIONS_KEY, ACCOUNTS_KEY]

export function useDecideDuplicate() {
  return useInvalidatingMutation(
    ({ pairId, verdict, keepId }: { pairId: Uuid; verdict: 'duplicate' | 'distinct'; keepId?: Uuid }) =>
      api.post<DuplicateDecision>(`/transaction-duplicates/${pairId}/decide`, {
        verdict,
        keep_id: keepId ?? null,
      }),
    DUPLICATE_WRITE,
    { failure: 'That answer was not saved' },
  )
}

/** Looks over the whole history, for rows that were there before the check was. */
export function useScanDuplicates() {
  return useInvalidatingMutation(
    () => api.post<DuplicateScan>('/transaction-duplicates/scan'),
    [DUPLICATES_KEY],
    { failure: false },
  )
}
