/**
 * Rules. A rule's conditions are a stored `Filter`, not a vocabulary of its
 * own. Match on `statement_name`: a rule matched on the payee stops firing once
 * its own rename lands. Nothing reaches existing rows without a preview, and
 * apply sends the previewed ids so a row synced in between is not swept in.
 */

import { useQuery } from '@tanstack/react-query'

import { api, type Money, type MoneyShape } from '@/lib/api'
import { useInvalidatingMutation } from '@/lib/queryClient'
import { TRANSACTIONS_KEY } from '@/lib/transactions/cache'
import type { FilterItemWrite, FilterField, IsoDate, Uuid } from '@/lib/transactions/types'

export type RuleField = FilterField

export type RuleCondition = Omit<FilterItemWrite, 'field'> & { field: RuleField }

export interface RuleFilter {
  id: Uuid
  name: string | null
  scope: string
  query_text: string | null
  items: (RuleCondition & { id: Uuid })[]
}

/** Null means "leave alone", which for the flags is not `false`. */
export interface RuleActions {
  set_payee: string | null
  set_category_id: Uuid | null
  add_tag_ids: Uuid[]
  set_notes: string | null
  set_excluded_from_reports: boolean | null
  set_excluded_from_spending_plan: boolean | null
  set_is_reviewed: boolean | null
}

export interface Rule {
  id: Uuid
  name: string
  filter_id: Uuid
  filter: RuleFilter
  /** A shared filter may not be edited in place. */
  owns_filter: boolean
  /** Lowest first; the first rule to set a field wins. */
  priority: number
  is_active: boolean
  actions: RuleActions
}

export interface RuleWrite {
  name?: string
  filter_id?: Uuid
  conditions?: RuleCondition[]
  is_active?: boolean
  actions?: Partial<RuleActions>
}

export interface RuleCreate extends RuleWrite {
  name: string
  conditions: RuleCondition[]
}

/** `actions` carries only the fields that actually change. */
export interface RuleChange {
  transaction_id: Uuid
  date: IsoDate
  account_name: string
  statement_name: string
  /** Before the rename. */
  payee: string
  amount: Money
  actions: RuleActions
}

export interface RulePreview {
  rule_id: Uuid
  matched: number
  /** The subset the actions actually move. */
  changed: number
  unchanged: number
  /** The list was capped; the counts were not. */
  truncated: boolean
  since: IsoDate | null
  changes: RuleChange[]
}

export interface RuleApplied {
  rule_id: Uuid
  matched: number
  applied: number
}

const PREVIEW_SHAPE: MoneyShape<RulePreview> = { changes: { amount: 'money' } }

const RULES_KEY = ['rules'] as const

export function useRules() {
  return useQuery({ queryKey: RULES_KEY, queryFn: ({ signal }) => api.get<Rule[]>('/rules', undefined, signal) })
}

/** Never cached: the apply is bounded by exactly the ids this showed. */
export function useRulePreview(id: Uuid | null) {
  return useQuery({
    queryKey: [...RULES_KEY, id ?? 'none', 'preview'],
    enabled: id !== null,
    gcTime: 0,
    staleTime: 0,
    queryFn: ({ signal }) =>
      api.get<RulePreview>(`/rules/${id ?? ''}/preview`, PREVIEW_SHAPE, signal),
  })
}

export function useCreateRule() {
  return useInvalidatingMutation(
    (body: RuleCreate) => api.post<Rule>('/rules', body),
    [RULES_KEY],
    { failure: 'That rule was not created' },
  )
}

export function useUpdateRule() {
  return useInvalidatingMutation(
    ({ id, patch }: { id: Uuid; patch: RuleWrite }) => api.patch<Rule>(`/rules/${id}`, patch),
    [RULES_KEY],
  )
}

/** Soft delete; rows it already touched keep its changes. */
export function useDeleteRule() {
  return useInvalidatingMutation(
    (id: Uuid) => api.delete<void>(`/rules/${id}`),
    [RULES_KEY],
    { failure: 'That rule was not deleted' },
  )
}

/** The whole live set; partial lists are refused. */
export function useReorderRules() {
  return useInvalidatingMutation(
    (ruleIds: Uuid[]) => api.post<Rule[]>('/rules/reorder', { rule_ids: ruleIds }),
    [RULES_KEY],
  )
}

export function useSetRuleActive() {
  return useInvalidatingMutation(
    ({ id, active }: { id: Uuid; active: boolean }) =>
      api.patch<Rule>(`/rules/${id}`, { is_active: active }),
    [RULES_KEY],
  )
}

export function useApplyRule() {
  return useInvalidatingMutation(
    ({ id, transactionIds }: { id: Uuid; transactionIds: Uuid[] }) =>
      api.post<RuleApplied>(`/rules/${id}/apply`, { transaction_ids: transactionIds }),
    [RULES_KEY, TRANSACTIONS_KEY],
  )
}
