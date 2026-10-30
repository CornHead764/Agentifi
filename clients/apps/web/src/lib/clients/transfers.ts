/**
 * Transfer pairs. Both legs drop out of income and expense, so a wrong pair
 * hides real spending, and a half pair (partner deleted) does so forever while
 * never matching again — `orphan_count` and the repair exist for that.
 */

import { useQuery } from '@tanstack/react-query'

import { api, type Money, type MoneyShape } from '@/lib/api'
import { useInvalidatingMutation, type Invalidates } from '@/lib/queryClient'
import { createTransaction } from '@/lib/transactions/api'
import { TRANSACTIONS_KEY } from '@/lib/transactions/cache'
import type { TransferPlan } from '@/lib/transactions/transferMoney'
import type { Uuid } from '@/lib/transactions/types'

export interface TransferLeg {
  transaction_id: Uuid
  account_id: Uuid
  account_name: string
  /**
   * The posted date, not the effective one: a card payment's two legs can be a
   * statement cycle apart by effective date.
   */
  date: string
  amount: Money
  currency: string
  payee: string
  source: string
  /** Set on an orphaned leg; null on a candidate for pairing. */
  pair_id: Uuid | null
}

export interface Transfer {
  pair_id: Uuid
  /** The later of the two posted dates. */
  moved_on: string
  /** A magnitude, from the paying leg. */
  amount: Money
  currency: string
  from: TransferLeg
  to: TransferLeg
  paired_by_hand: boolean
}

export interface TransferWindow {
  from: string | null
  to: string | null
  date_field: string
}

export interface TransferList {
  transfers: Transfer[]
  window: TransferWindow
  /** Over the whole space, not the window. */
  orphan_count: number
}

export interface TransferCandidateList {
  candidates: TransferLeg[]
  window: TransferWindow
}

export interface OrphanList {
  orphans: TransferLeg[]
}

const LEG_SHAPE: MoneyShape<TransferLeg> = { amount: 'money' }

const TRANSFER_SHAPE: MoneyShape<Transfer> = {
  amount: 'money',
  from: LEG_SHAPE,
  to: LEG_SHAPE,
}

const LIST_SHAPE: MoneyShape<TransferList> = { transfers: TRANSFER_SHAPE }
const CANDIDATES_SHAPE: MoneyShape<TransferCandidateList> = { candidates: LEG_SHAPE }
const ORPHANS_SHAPE: MoneyShape<OrphanList> = { orphans: LEG_SHAPE }

export const TRANSFERS_KEY = ['transfers'] as const
const TRANSFER_CANDIDATES_KEY = ['transfers', 'candidates'] as const
export const TRANSFER_ORPHANS_KEY = ['transfers', 'orphans'] as const

export function useTransfers() {
  return useQuery({
    queryKey: TRANSFERS_KEY,
    queryFn: ({ signal }) => api.get<TransferList>('/transfers', LIST_SHAPE, signal),
  })
}

export function useTransferCandidates(enabled: boolean) {
  return useQuery({
    queryKey: TRANSFER_CANDIDATES_KEY,
    queryFn: ({ signal }) =>
      api.get<TransferCandidateList>('/transfers/candidates', CANDIDATES_SHAPE, signal),
    // Every unpaired row in the ledger, so only while the dialog is open.
    enabled,
  })
}

export function useOrphanLegs(enabled: boolean) {
  return useQuery({
    queryKey: TRANSFER_ORPHANS_KEY,
    queryFn: ({ signal }) => api.get<OrphanList>('/transfers/orphans', ORPHANS_SHAPE, signal),
    enabled,
  })
}

/** Every write changes whether rows count as income and expense, so the register refreshes too. */
const TRANSFER_WRITE: Invalidates = [TRANSFERS_KEY, TRANSACTIONS_KEY]

/** Both transactions stay as ordinary rows. */
export function useUnpairTransfer() {
  return useInvalidatingMutation(
    (pairId: Uuid) => api.delete<void>(`/transfers/${pairId}`),
    TRANSFER_WRITE,
  )
}

function pairByHand({ payingId, receivingId }: { payingId: Uuid; receivingId: Uuid }) {
  return api.post<Transfer>('/transfers', {
    paying_transaction_id: payingId,
    receiving_transaction_id: receivingId,
  })
}

export function usePairByHand() {
  return useInvalidatingMutation(pairByHand, TRANSFER_WRITE)
}

/**
 * Writes both legs, then pairs them. Its caller says the failure itself,
 * because either leg may already be in the register by then.
 */
export function useRecordTransfer() {
  return useInvalidatingMutation(
    async ({ paying, receiving }: TransferPlan) => {
      const out = await createTransaction(paying)
      const into = await createTransaction(receiving)
      return pairByHand({ payingId: out.id, receivingId: into.id })
    },
    TRANSFER_WRITE,
    { failure: false },
  )
}

/** Sweeps the whole ledger; a sync pairs only the rows it just wrote. */
export function useDetectTransfers() {
  return useInvalidatingMutation(
    () => api.post<{ paired: number }>('/transfers/detect'),
    TRANSFER_WRITE,
    { failure: false },
  )
}

/** Safe to run repeatedly. */
export function useRepairOrphanLegs() {
  return useInvalidatingMutation(
    () => api.post<OrphanList>('/transfers/orphans/repair', undefined, ORPHANS_SHAPE),
    TRANSFER_WRITE,
  )
}
