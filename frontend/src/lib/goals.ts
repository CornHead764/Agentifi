/**
 * Savings goals on the client; shapes mirror `backend/internal/domain/goals.go`
 * and the figures are defined in docs/calculations.md §6. A goal holds
 * no ledger of its own: `saved_so_far` inflates the `goal_balance` of the one
 * account it names, which subtracts it from that account's available balance.
 */

import { useQuery } from '@tanstack/react-query'

import { api, type MoneyShape } from './api'
import type { WireRate } from './clients/entities'
import { formatDate, parseRate, plural } from './format'
import {
  type Money,
  type MoneyFormatter,
  ZERO_MONEY,
  addMoney,
  amountToWire,
  moneyToNumber,
  parseAmountInput,
} from './money'
import { useInvalidatingMutation, type Invalidates } from './queryClient'
import { matchesSearch } from './search'
import { ACCOUNTS_KEY, TRANSACTIONS_KEY } from './transactions/cache'
import { spendingPlanKeys } from './spendingPlan/api'

/** One account's part in a goal. The rows sum to `saved_so_far`, so a dropped funding account still appears. */
export interface GoalFunding {
  account_id: string
  account_name: string
  /** Positive. */
  saved: Money
}

/** `amount` is the ledger's sign; `saved` is positive for money set aside, negative for money taken out. */
export interface GoalContribution {
  transaction_id: string
  /**
   * Recorded when the row was linked, never read off `amount`. `spending`
   * does not move the reserve: its `saved` is zero and `spent` carries it.
   */
  kind: GoalKind
  date: string
  account_id: string
  account_name: string
  payee: string
  amount: Money
  saved: Money
  spent: Money
}

/** What a goal's money went on, by the rows' own categories; sums to `spent_on_goal`, refunds included. */
export interface GoalCategorySpend {
  /** `null` for uncategorized rows, which are listed, not dropped. */
  category_id: string | null
  category_name: string
  spent: Money
  /** A split row counts once per category it touches. */
  transaction_count: number
}

export interface Goal {
  id: string
  name: string
  emoji: string | null
  /** The account whose reserve this goal inflates — one, however many fund it. */
  account_id: string
  account_name: string
  /** Every account the goal may draw on. */
  funding_account_ids: string[]
  funding: GoalFunding[]
  target_amount: Money
  /** `null` for an open-ended goal, which has no required rate. */
  target_on: string | null
  /** Only set on goals imported from Simplifi; nothing here stamps one. */
  completed_on: string | null
  /** The day the user closed it; `null` while open. A closed goal reserves nothing. */
  closed_on: string | null
  /** Where the goal is in its life; the card is drawn by it. */
  stage: GoalStage
  /** A goal funded from money the plan already counted must not be counted twice. */
  is_taken_from_plan: boolean
  /** Contributions less withdrawals. Not clamped: a raided goal is a real state. */
  saved_so_far: Money
  /** Positive. Simplifi labels this "Spent". */
  withdrawn: Money
  /**
   * Net of refunds. Not subtracted from `saved_so_far` and not comparable to
   * `withdrawn`: the same purchase is usually in both.
   */
  spent_on_goal: Money
  /** Largest first. */
  spending_by_category: GoalCategorySpend[]
  /** `withdrawn` less `spent_on_goal`, never negative: taken out, not yet linked to a purchase. */
  unassigned_withdrawn: Money
  /**
   * `saved_so_far` plus `withdrawn`, the length of the progress bar, so a goal
   * saved and then spent still shows its target met.
   */
  funded: Money
  contributed_this_month: Money

  // Server-computed from the same pass; the client never recomputes them.
  /** Read only while `stage` is `saving`: a funded goal has nothing left to save. */
  left_to_save: Money
  /** `null` without a target date, and once the goal is funded or closed. */
  monthly_needed: Money | null
  /**
   * The months `monthly_needed` was divided by, this one included. From the
   * server because the browser's clock is the viewer's, not the server's.
   */
  months_to_target: number | null
  /** `months_to_target` clamps a past date to one month; this says the date has passed. */
  target_has_passed: boolean
  /** A percentage, 0–100, clamped for the bar; `null` for a target of zero. */
  pct_complete: WireRate
  /** The same over `funded`; the gap between the two is money saved then taken out. */
  pct_funded: WireRate
  is_complete: boolean
  /** Whether the target was ever reached, money spent since included. */
  is_funded: boolean
  /** Every row counted, deleted ones included; `contributions` is the live ones. */
  txn_ids: string[]
  withdrawal_txn_ids: string[]
  /** Rows that record where the money went without moving the reserve. */
  spending_txn_ids: string[]
  /** Newest first; deleted rows are not listed. */
  contributions: GoalContribution[]
}

export const GOAL_SHAPE: MoneyShape<Goal> = {
  target_amount: 'money',
  saved_so_far: 'money',
  withdrawn: 'money',
  spent_on_goal: 'money',
  spending_by_category: { spent: 'money' },
  unassigned_withdrawn: 'money',
  funded: 'money',
  contributed_this_month: 'money',
  left_to_save: 'money',
  monthly_needed: 'money',
  funding: { saved: 'money' },
  contributions: { amount: 'money', saved: 'money', spent: 'money' },
}

/*
 * Derived goal figures are not recomputed here: a client copy would count
 * months off the browser's clock, not the server's.
 */

/**
 * The goal bar's two segments, both percentages of the target: what is still
 * set aside, and what was set aside and has since been spent, which is the gap
 * between `pct_funded` and `pct_complete`. The gap is taken in hundredths of a
 * percent, so two figures off the wire do not subtract to 0.0999…. `reached` is
 * the length the two fill together, `null` for a target of zero. Filling to
 * `pct_complete` alone would walk a goal back to nothing as its money is spent.
 */
export function goalBarSegments(goal: Pick<Goal, 'pct_complete' | 'pct_funded'>): {
  saved: number
  spent: number
  reached: number | null
} {
  const saved = hundredths(goal.pct_complete)
  const funded = hundredths(goal.pct_funded)
  const savedLength = saved === null ? 0 : Math.min(Math.max(saved, 0), 10000)
  const spentLength =
    saved === null || funded === null
      ? 0
      : Math.min(Math.max(funded - savedLength, 0), 10000 - savedLength)
  return {
    saved: savedLength / 100,
    spent: spentLength / 100,
    reached: funded === null ? null : funded / 100,
  }
}

function hundredths(rate: WireRate): number | null {
  const parsed = parseRate(rate)
  return parsed === null ? null : Math.round(parsed * 100)
}

/** Only open goals that *name* the account, however many fund them; a closed goal reserves nothing. */
export function reservedInAccount(accountId: string, goals: Goal[]): Money {
  let total = ZERO_MONEY
  for (const goal of goals) {
    if (goal.account_id === accountId && isOpenGoal(goal)) {
      total = addMoney(total, goal.saved_so_far)
    }
  }
  return total
}

/**
 * `saving` until the target is reached, `funded` once it is with nothing taken
 * out, `spending` once money was taken out or spent on it, `closed` once the
 * user closed it. See calculations.md §6.
 */
export type GoalStage = 'saving' | 'funded' | 'spending' | 'closed'

/** Rows can be counted toward any goal the user has not closed. */
export function isOpenGoal(goal: Pick<Goal, 'stage'>): boolean {
  return goal.stage !== 'closed'
}

/** A row the goal is likely missing, best first, from `GET /goals/{id}/suggestions`. */
export interface GoalSuggestion {
  transaction_id: string
  date: string
  account_id: string
  account_name: string
  payee: string
  amount: Money
  /** `Split` for a split row, empty for an uncategorized one. */
  category_name: string
  matches_category: boolean
  matches_payee: boolean
  /** Days to the goal's nearest withdrawal; `null` when it has none. */
  days_from_withdrawal: number | null
}

/** A suggestion's date and account, then why it ranks where it does. */
export function describeGoalSuggestion(row: GoalSuggestion): string {
  const parts = [formatDate(row.date, 'short'), row.account_name]
  if (row.category_name) parts.push(row.category_name)
  if (row.matches_category) parts.push('same category as its spending')
  else if (row.matches_payee) parts.push('same payee as its spending')
  if (row.days_from_withdrawal !== null) {
    parts.push(
      row.days_from_withdrawal === 0
        ? 'same day as a withdrawal'
        : `${plural(row.days_from_withdrawal, 'day')} from a withdrawal`,
    )
  }
  return parts.join(' · ')
}

export interface GoalSuggestions {
  kind: GoalKind
  /** The dates searched; `null` when the goal has no rows to search around. */
  from: string | null
  to: string | null
  rows: GoalSuggestion[]
  truncated: boolean
}

const GOALS_KEY = ['goals'] as const

const goalsApi = {
  list: (signal?: AbortSignal) => api.get<Goal[]>('/goals', GOAL_SHAPE, signal),

  /** Funding goes as ids, never amounts: the ledger is the only source of a savings total. */
  create: (body: {
    name: string
    emoji?: string | null
    target_amount: string
    target_on: string | null
    account_id: string
    is_taken_from_plan?: boolean
    funding_account_ids?: string[]
    txn_ids?: string[]
    withdrawal_txn_ids?: string[]
    spending_txn_ids?: string[]
  }) => api.post<Goal>('/goals', body, GOAL_SHAPE),

  update: (
    id: string,
    body: {
      name?: string
      emoji?: string | null
      account_id?: string
      target_amount?: string
      target_on?: string | null
      is_taken_from_plan?: boolean
      funding_account_ids?: string[]
      txn_ids?: string[]
      withdrawal_txn_ids?: string[]
      spending_txn_ids?: string[]
    },
  ) => api.patch<Goal>(`/goals/${id}`, body, GOAL_SHAPE),

  remove: (id: string) => api.delete<void>(`/goals/${id}`),

  close: (id: string) => api.post<Goal>(`/goals/${id}/close`, undefined, GOAL_SHAPE),

  reopen: (id: string) => api.post<Goal>(`/goals/${id}/reopen`, undefined, GOAL_SHAPE),

  suggestions: (id: string, move: GoalMove, signal?: AbortSignal) =>
    api.get<GoalSuggestions>(
      `/goals/${id}/suggestions?kind=${GOAL_KIND_OF[move]}`,
      { rows: { amount: 'money' } },
      signal,
    ),

  /**
   * The direction is sent, never inferred: the row looks the same whether it
   * funded the goal or paid for what it saved toward. Resending with another
   * direction re-files it; a row counted toward another goal is refused.
   */
  link: (id: string, transactionId: string, move: GoalMove) =>
    api.post<Goal>(
      `/goals/${id}/transactions`,
      { transaction_id: transactionId, direction: GOAL_KIND_OF[move] },
      GOAL_SHAPE,
    ),

  /** Partial: rows already counting toward another goal come back in `skipped`. */
  linkMany: (id: string, transactionIds: string[], move: GoalMove) =>
    api.post<GoalBulkLink>(
      `/goals/${id}/transactions/bulk`,
      { transaction_ids: transactionIds, direction: GOAL_KIND_OF[move] },
      { goal: GOAL_SHAPE },
    ),

  unlink: (id: string, transactionId: string) =>
    api.delete<Goal>(`/goals/${id}/transactions/${transactionId}`, GOAL_SHAPE),
}

/** `spend` records where the money went and leaves `saved_so_far` alone. */
export type GoalMove = 'contribute' | 'withdraw' | 'spend'

export type GoalKind = 'contribution' | 'withdrawal' | 'spending'

export interface GoalBulkLink {
  goal: Goal
  linked: number
  skipped: { transaction_id: string; reason: string }[]
}

const GOAL_KIND_OF: Record<GoalMove, GoalKind> = {
  contribute: 'contribution',
  withdraw: 'withdrawal',
  spend: 'spending',
}

/** The toast after one row is counted toward `goal`, with the goal's new figure. */
export function goalCountedMessage(
  goal: Goal,
  move: GoalMove,
  money: MoneyFormatter,
): { title: string; description: string } {
  return {
    title:
      move === 'contribute'
        ? `Counted toward ${goal.name}`
        : move === 'spend'
          ? `Recorded as spending on ${goal.name}`
          : `Taken out of ${goal.name}`,
    description:
      move === 'spend'
        ? `${money(goal.spent_on_goal)} spent on it so far.`
        : `${money(goal.saved_so_far)} saved so far.`,
  }
}

/** What a row counted toward a goal is, in the words the pickers and the detail use. */
export const GOAL_MOVE_LABELS: Record<GoalMove, string> = {
  contribute: 'A contribution',
  withdraw: 'A withdrawal',
  spend: 'Spending on it',
}

export function goalMoveOfKind(kind: GoalKind): GoalMove {
  return kind === 'withdrawal' ? 'withdraw' : kind === 'spending' ? 'spend' : 'contribute'
}

/**
 * The goal one row counts toward, and which way. A counted row's `move` is
 * what the goal recorded, never the sign; an uncounted row's is the suggestion
 * the sign supports.
 */
export function goalCounting(
  txn: { id: string; amount: Money },
  goals: readonly Goal[],
): { goal: Goal | null; move: GoalMove | null } {
  const goal = goals.find((one) => one.txn_ids.includes(txn.id)) ?? null
  if (goal) {
    const kind = goal.spending_txn_ids.includes(txn.id)
      ? 'spending'
      : goal.withdrawal_txn_ids.includes(txn.id)
        ? 'withdrawal'
        : 'contribution'
    return { goal, move: goalMoveOfKind(kind) }
  }
  return { goal: null, move: txn.amount < 0 ? 'contribute' : txn.amount > 0 ? 'withdraw' : null }
}

/**
 * The rows a goal could count. The sign only narrows rows on the reserve
 * account; on other funding accounts it decides nothing. `spend` is narrowed
 * by neither (refunds must be linkable). Rows in this goal are left out; rows
 * in another goal are kept and marked.
 */
export function goalCandidates<
  T extends {
    id: string
    account_id: string
    amount: Money
    payee: string
    statement_name: string
  },
>(
  rows: readonly T[],
  move: GoalMove,
  search: string,
  goals: readonly Goal[],
  goal: { id: string; account_id: string },
) {
  const owner = new Map<string, Goal>()
  for (const one of goals) {
    for (const id of one.txn_ids) owner.set(id, one)
  }
  const out: { row: T; elsewhere: Goal | null }[] = []
  for (const row of rows) {
    if (row.amount === 0) continue
    if (row.account_id === goal.account_id) {
      if (move === 'contribute' && row.amount < 0) continue
      if (move === 'withdraw' && row.amount > 0) continue
    }
    const counted = owner.get(row.id) ?? null
    if (counted?.id === goal.id) continue
    const dollars = String(Math.trunc(Math.abs(moneyToNumber(row.amount))))
    if (!matchesSearch(search, row.payee, row.statement_name, dollars)) continue
    out.push({ row, elsewhere: counted })
  }
  return out
}

type GoalWrite = Parameters<typeof goalsApi.create>[0]

/** The goal editor's form state. `target_amount` stays raw text until submit, never a float. */
export interface GoalEdits {
  name: string
  emoji: string
  account_id: string
  /** A join, not a second foreign key: `account_id` alone holds the reserve. */
  funding_account_ids: string[]
  target_amount: string
  target_on: string | null
  is_taken_from_plan: boolean
}

/** A template seeds a name and glyph only; nothing downstream branches on it. */
export interface GoalTemplate {
  key: string
  label: string
  emoji: string
}

export const GOAL_TEMPLATES: readonly GoalTemplate[] = [
  { key: 'emergency', label: 'Emergency Fund', emoji: '☔' },
  { key: 'car', label: 'Car', emoji: '🚗' },
  { key: 'vacation', label: 'Vacation', emoji: '🏖️' },
  { key: 'home', label: 'Home', emoji: '🏡' },
  { key: 'wedding', label: 'Wedding', emoji: '💍' },
  { key: 'custom', label: 'Custom', emoji: '💰' },
]

/** `is_taken_from_plan` defaults to true when a create omits it. */
export function blankGoalEdits(template?: GoalTemplate): GoalEdits {
  return {
    // Custom means "none of these", so it seeds no name.
    name: template && template.key !== 'custom' ? template.label : '',
    emoji: template?.emoji ?? '',
    account_id: '',
    funding_account_ids: [],
    target_amount: '0.00',
    target_on: null,
    is_taken_from_plan: true,
  }
}

export function editsFromGoal(goal: Goal): GoalEdits {
  return {
    name: goal.name,
    emoji: goal.emoji ?? '',
    account_id: goal.account_id,
    funding_account_ids: goal.funding_account_ids,
    target_amount: amountToWire(goal.target_amount),
    target_on: goal.target_on,
    is_taken_from_plan: goal.is_taken_from_plan,
  }
}

/**
 * The create/update body, or `null` for an amount that does not parse.
 * `txn_ids` is left off rather than sent empty, which would clear it.
 */
export function goalBodyFromEdits(edits: GoalEdits): GoalWrite | null {
  const amount = parseAmountInput(edits.target_amount)
  if (amount === null) return null
  return {
    name: edits.name.trim(),
    emoji: edits.emoji.trim() || null,
    account_id: edits.account_id,
    funding_account_ids: fundingAccounts(edits),
    target_amount: amount.wire,
    target_on: edits.target_on || null,
    is_taken_from_plan: edits.is_taken_from_plan,
  }
}

/** The account holding the reserve is always one of the funding accounts. */
export function fundingAccounts(edits: GoalEdits): string[] {
  const ids = edits.funding_account_ids.filter((id) => id !== edits.account_id)
  return edits.account_id ? [edits.account_id, ...ids] : ids
}

/** A goal inflates its account's `goal_balance`, so every write refreshes the accounts too. */
const GOAL_WRITE: Invalidates = [GOALS_KEY, ACCOUNTS_KEY]

export function useCreateGoal() {
  return useInvalidatingMutation((body: GoalWrite) => goalsApi.create(body), GOAL_WRITE, {
    failure: 'That goal was not saved',
  })
}

export function useUpdateGoal() {
  return useInvalidatingMutation(
    ({ id, body }: { id: string; body: GoalWrite }) => goalsApi.update(id, body),
    GOAL_WRITE,
    { failure: 'That goal was not saved' },
  )
}

/** Closing releases the reserve and leaves the plan's Goals bucket, so both refresh. */
export function useCloseGoal() {
  return useInvalidatingMutation(
    ({ id, close }: { id: string; close: boolean }) =>
      close ? goalsApi.close(id) : goalsApi.reopen(id),
    [GOALS_KEY, ACCOUNTS_KEY, spendingPlanKeys.all],
    { failure: 'That goal was not changed' },
  )
}

export function useDeleteGoal() {
  return useInvalidatingMutation((id: string) => goalsApi.remove(id), GOAL_WRITE, {
    failure: 'That goal was not deleted',
  })
}

export function useLinkGoalTransaction() {
  return useInvalidatingMutation(
    ({ id, transactionId, move }: { id: string; transactionId: string; move: GoalMove }) =>
      goalsApi.link(id, transactionId, move),
    GOAL_WRITE,
    { failure: 'That goal link was not saved' },
  )
}

/** Spend on a goal leaves the spending plan, so the plan and the register refresh too. */
export function useLinkGoalTransactions() {
  return useInvalidatingMutation(
    ({ id, transactionIds, move }: { id: string; transactionIds: string[]; move: GoalMove }) =>
      goalsApi.linkMany(id, transactionIds, move),
    [GOALS_KEY, spendingPlanKeys.all, TRANSACTIONS_KEY],
    { failure: 'Those rows were not filed under the goal' },
  )
}

export function useUnlinkGoalTransaction() {
  return useInvalidatingMutation(
    ({ id, transactionId }: { id: string; transactionId: string }) =>
      goalsApi.unlink(id, transactionId),
    GOAL_WRITE,
    { failure: 'That goal link was not saved' },
  )
}

/** Rows the goal is likely missing, of one kind; `enabled` false while nothing asks. */
export function useGoalSuggestions(id: string, move: GoalMove, enabled = true) {
  return useQuery({
    queryKey: [...GOALS_KEY, id, 'suggestions', move],
    queryFn: ({ signal }) => goalsApi.suggestions(id, move, signal),
    enabled,
  })
}

export function useGoals() {
  return useQuery({ queryKey: GOALS_KEY, queryFn: ({ signal }) => goalsApi.list(signal) })
}
