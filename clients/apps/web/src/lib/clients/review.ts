import { useInfiniteQuery, useQuery, type QueryKey } from '@tanstack/react-query'

import {
  ActRequestSchema,
  ApproveRowsRequestSchema,
  ApproveRowsResponseSchema,
  FileRowRequestSchema,
  FileRowResponseSchema,
  GetReviewSummaryResponseSchema,
  ListReviewDaysRequestSchema,
  ListReviewDaysResponseSchema,
  ListReviewItemsResponseSchema,
  ReviewService,
  UndoReviewRequestSchema,
  UndoReviewResponseSchema,
} from '@/gen/agentifi/v1/review_pb'
import { useInvalidatingMutation } from '@/lib/queryClient'
import { fromWire, toWire } from '@/lib/rpc/wire'
import { rpcClient, unary } from '@/lib/rpcSession'
import { TRANSACTIONS_KEY } from '@/lib/transactions/cache'
import type { Money } from '@agentifi/core/money'

/** The review queue (calculations.md §15), in the app's shape. */

export const REVIEW_KEY: QueryKey = ['review']
const SUMMARY_KEY: QueryKey = [...REVIEW_KEY, 'summary']
const ITEMS_KEY: QueryKey = [...REVIEW_KEY, 'items']
const DAYS_KEY: QueryKey = [...REVIEW_KEY, 'days']

export type ReviewKind =
  | 'sign_in_code'
  | 'sign_in'
  | 'bank_reconnect'
  | 'bill_past_due'
  | 'statement_due'
  | 'duplicate'
  | 'orphan_transfers'
  | 'held_balance'
  | 'proposal'
  | 'recurring_suggestion'
  | 'unmatched_order'

export type ReviewTier = 'act_now' | 'money_at_stake' | 'tidy_up' | 'ideas'

/** Why a row needs a look; `unspecified` is a row that looks right. */
export type ReviewReason =
  | 'unspecified'
  | 'not_recognised'
  | 'possible_duplicate'
  | 'no_category'
  | 'guess'
  | 'unusual_amount'

export type ReviewAction =
  | 'keep_suggested'
  | 'keep_other'
  | 'both_real'
  | 'accept_balance'
  | 'repair_transfers'
  | 'mark_paid'
  | 'skip_occurrence'
  | 'dismiss_suggestion'
  | 'ignore_order'
  | 'answer_code'
  | 'accept_proposal'
  | 'decline_proposal'

export interface ReviewActionOption {
  action: ReviewAction
  label: string
  /** Hard to undo: asked first, never swiped or bulk. */
  confirm: boolean
}

export interface ReviewItem {
  key: string
  kind: ReviewKind
  tier: ReviewTier
  title: string
  detail: string
  due_at: string | null
  amount: Money | null
  subject_ids: string[]
  actions: ReviewActionOption[]
  link: string
  count: number
}

export interface ReviewSplit {
  category_id: string | null
  category_name: string
  amount: Money
  memo: string
}

export interface ReviewSuggestion {
  action_id: string
  category_id: string | null
  category_name: string
  confidence: number | null
  splits: ReviewSplit[]
  basis: string
}

export interface ReviewSupport {
  kind: string
  title: string
  lines: string[]
  link: string
}

export interface ReviewRow {
  transaction_id: string
  date: string
  effective_date: string
  payee: string
  statement_name: string
  amount: Money
  account_id: string
  account_name: string
  account_mask: string
  category_id: string | null
  category_name: string
  splits: ReviewSplit[]
  is_pending: boolean
  reason: ReviewReason
  suggestion: ReviewSuggestion | null
  support: ReviewSupport[]
  duplicate_key: string
  notes: string
}

export interface ReviewDay {
  date: string
  out: Money
  needs_a_look: number
  rows: ReviewRow[]
}

export interface ReviewSummary {
  act_now: number
  money_at_stake: number
  tidy_up: number
  ideas: number
  new_rows: number
  needs_a_look: number
  badge: number
  kinds: { kind: ReviewKind; count: number }[]
  oldest_open_day: string | null
}

export function looksRight(row: Pick<ReviewRow, 'reason'>): boolean {
  return row.reason === 'unspecified'
}

const client = rpcClient(ReviewService)

export function useReviewSummary() {
  return useQuery({
    queryKey: SUMMARY_KEY,
    queryFn: () =>
      unary(ReviewService.method.getReviewSummary, async () =>
        fromWire<ReviewSummary>(GetReviewSummaryResponseSchema, await client.getReviewSummary({})),
      ),
  })
}

export function useReviewItems() {
  return useQuery({
    queryKey: ITEMS_KEY,
    queryFn: () =>
      unary(ReviewService.method.listReviewItems, async () =>
        fromWire<{ items: ReviewItem[] }>(ListReviewItemsResponseSchema, await client.listReviewItems({})).items,
      ),
  })
}

const DAYS_PER_PAGE = 7

export function useReviewDays() {
  return useInfiniteQuery({
    queryKey: DAYS_KEY,
    initialPageParam: null as string | null,
    queryFn: ({ pageParam }) =>
      unary(ReviewService.method.listReviewDays, async () =>
        fromWire<{ days: ReviewDay[]; next_before: string | null }>(
          ListReviewDaysResponseSchema,
          await client.listReviewDays(
            toWire(ListReviewDaysRequestSchema, { before: pageParam, days: DAYS_PER_PAGE }),
          ),
        ),
      ),
    getNextPageParam: (page) => page.next_before,
  })
}

/** Every review write leaves the queue and the register stale. */
const STALE = [REVIEW_KEY, TRANSACTIONS_KEY] as const

export interface ApproveResult {
  approved_ids: string[]
  skipped: ReviewRow[]
  undo_token: string
}

export function useApproveRows() {
  return useInvalidatingMutation(
    (args: { transaction_ids: string[]; only_if_looks_right?: boolean }) =>
      unary(ReviewService.method.approveRows, async () =>
        fromWire<ApproveResult>(
          ApproveRowsResponseSchema,
          await client.approveRows(toWire(ApproveRowsRequestSchema, { only_if_looks_right: false, ...args })),
        ),
      ),
    STALE,
    { failure: 'Could not approve' },
  )
}

export function useFileRow() {
  return useInvalidatingMutation(
    (args: { transaction_id: string; category_id: string }) =>
      unary(ReviewService.method.fileRow, async () =>
        fromWire<{ undo_token: string }>(
          FileRowResponseSchema,
          await client.fileRow(toWire(FileRowRequestSchema, args)),
        ),
      ),
    STALE,
    { failure: 'Could not file the row' },
  )
}

export function useUndoReview() {
  return useInvalidatingMutation(
    (undo_token: string) =>
      unary(ReviewService.method.undoReview, async () =>
        fromWire<{ restored_ids: string[]; changed_ids: string[] }>(
          UndoReviewResponseSchema,
          await client.undoReview(toWire(UndoReviewRequestSchema, { undo_token })),
        ),
      ),
    STALE,
    { failure: 'Could not undo' },
  )
}

export function useReviewAct() {
  return useInvalidatingMutation(
    (args: { key: string; action: ReviewAction; text?: string }) =>
      unary(ReviewService.method.act, async () => {
        await client.act(toWire(ActRequestSchema, { text: '', ...args }))
      }),
    STALE,
    { failure: 'Could not do that' },
  )
}
