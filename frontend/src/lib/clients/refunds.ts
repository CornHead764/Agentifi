/**
 * Refund links: which charge a credit gives back, for a credit its category
 * cannot place. `can_be_a_refund` is the server's rule and is not duplicated here.
 */

import { useQuery } from '@tanstack/react-query'

import { api, type Money, type MoneyShape } from '@/lib/api'
import { useInvalidatingMutation, type Invalidates } from '@/lib/queryClient'
import { TRANSACTIONS_KEY } from '@/lib/transactions/cache'
import type { Uuid } from '@/lib/transactions/types'

export interface RefundCharge {
  id: Uuid
  account_id: Uuid
  account_name: string
  /** The posted date, as the register shows it. */
  date: string
  amount: Money
  payee: string
  statement_name: string
  category_id: Uuid | null
  category_name: string | null
}

/** `refunds` is what this row gives back; `refunded_by` is what gives this row back. */
export interface RefundLinks {
  can_be_a_refund: boolean
  refunds: RefundCharge[]
  refunded_by: RefundCharge[]
}

export interface RefundCandidateList {
  candidates: RefundCharge[]
}

const CHARGE_SHAPE: MoneyShape<RefundCharge> = { amount: 'money' }

const LINKS_SHAPE: MoneyShape<RefundLinks> = {
  refunds: CHARGE_SHAPE,
  refunded_by: CHARGE_SHAPE,
}

const CANDIDATES_SHAPE: MoneyShape<RefundCandidateList> = { candidates: CHARGE_SHAPE }

const REFUNDS_ROOT = ['refunds'] as const

export function refundLinksKey(id: Uuid | null) {
  return ['refunds', 'links', id] as const
}

export function refundCandidatesKey(id: Uuid | null, search: string) {
  return ['refunds', 'candidates', id, search] as const
}

function getRefundLinks(id: Uuid, signal?: AbortSignal) {
  return api.get<RefundLinks>(`/refunds/transactions/${id}`, LINKS_SHAPE, signal)
}

function listRefundCandidates(id: Uuid, search: string, signal?: AbortSignal) {
  const query = search.trim() === '' ? '' : `?q=${encodeURIComponent(search.trim())}`
  return api.get<RefundCandidateList>(
    `/refunds/transactions/${id}/candidates${query}`,
    CANDIDATES_SHAPE,
    signal,
  )
}

function linkRefund(refundId: Uuid, chargeId: Uuid) {
  return api.post<RefundLinks>(
    `/refunds/transactions/${refundId}/charges`,
    { charge_transaction_id: chargeId },
    LINKS_SHAPE,
  )
}

function unlinkRefund(refundId: Uuid, chargeId: Uuid) {
  return api.delete<void>(`/refunds/transactions/${refundId}/charges/${chargeId}`)
}

const NO_LINKS: RefundLinks = { can_be_a_refund: false, refunds: [], refunded_by: [] }

export function useRefundLinks(id: Uuid | null) {
  return useQuery({
    queryKey: refundLinksKey(id),
    queryFn: ({ signal }) => (id === null ? Promise.resolve(NO_LINKS) : getRefundLinks(id, signal)),
    enabled: id !== null,
  })
}

/**
 * Without a search the server answers with the hundred days before the credit;
 * with one it reaches the whole history.
 */
export function useRefundCandidates(id: Uuid | null, search: string, enabled: boolean) {
  return useQuery({
    queryKey: refundCandidatesKey(id, search),
    queryFn: ({ signal }) =>
      id === null ? Promise.resolve({ candidates: [] }) : listRefundCandidates(id, search, signal),
    enabled: enabled && id !== null,
  })
}

/** A link moves money between category totals, so the register refreshes too. */
const REFUND_WRITE: Invalidates = [REFUNDS_ROOT, TRANSACTIONS_KEY]

export function useLinkRefund() {
  return useInvalidatingMutation(
    ({ refundId, chargeId }: { refundId: Uuid; chargeId: Uuid }) => linkRefund(refundId, chargeId),
    REFUND_WRITE,
  )
}

export function useUnlinkRefund() {
  return useInvalidatingMutation(
    ({ refundId, chargeId }: { refundId: Uuid; chargeId: Uuid }) =>
      unlinkRefund(refundId, chargeId),
    REFUND_WRITE,
  )
}
