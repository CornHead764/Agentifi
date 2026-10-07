/**
 * Bills and income, cash flow, the spending plan, goals, watchlists, the
 * retirement planner, rules and guidance for the Sample household. Invented.
 */

import { ACCOUNT, CATEGORY, TAG, accountName, categoryName, id, parentOf } from './core'
import { SHOWCASE, TODAY_ISO, TRANSACTIONS, byNumber, isSpending, money, sum, type Transaction } from './ledger'

type Kind = 'income' | 'bill' | 'subscription' | 'transfer' | 'credit_card_payment' | 'refund'

const MONTHLY = { frequency: 'MONTHLY', interval: 1, by_day: [], by_month: [] }

interface SeriesSeed {
  n: number
  kind: Kind
  description: string
  displayName?: string
  amount: string
  day: number
  account?: string
  category: string | null
  recurrence?: object
  perYear?: number
  tags?: string[]
}

function series(seed: SeriesSeed) {
  const perYear = seed.perYear ?? 12
  const next = `2026-${seed.day >= 15 ? '06' : '07'}-${String(seed.day).padStart(2, '0')}`
  return {
    id: id('series', seed.n),
    account_id: seed.account ?? ACCOUNT.checking,
    category_id: seed.category,
    kind: seed.kind,
    description: seed.description,
    display_name: seed.displayName ?? null,
    label: seed.displayName ?? seed.description,
    amount: seed.amount,
    currency: 'USD',
    recurrence: seed.recurrence ?? { alias: 'EVERY_MONTH', ...MONTHLY, by_month_day: [seed.day] },
    start_on: '2025-01-01',
    end_on: null,
    next_due_on: next,
    due_on: next,
    override_next_due_on: null,
    override_next_amount: null,
    auto_adjust_due_on: false,
    reminder_days: 3,
    match_criteria: 'auto',
    match_amount_min: null,
    match_amount_max: null,
    tag_ids: seed.tags ?? [],
    splits: [],
    is_active: true,
    annualized_amount: (Number(seed.amount) * perYear).toFixed(2),
    occurrences_per_year: perYear,
  }
}

const PAYCHECK = series({ n: 1, kind: 'income', description: 'EXAMPLE EMPLOYER PAYROLL', displayName: 'Example Employer Payroll', amount: '3250.00', day: 15, category: CATEGORY.paycheck, recurrence: { alias: 'TWICE_A_MONTH', ...MONTHLY, by_month_day: [1, 15] }, perYear: 24 })
const MORTGAGE = series({ n: 2, kind: 'bill', description: 'SAMPLE CU MORTGAGE PMT', displayName: 'Mortgage Payment', amount: '-1850.00', day: 1, category: CATEGORY.mortgage })
const AUTO_LOAN = series({ n: 3, kind: 'bill', description: 'SAMPLE CU AUTO LOAN PMT', displayName: 'Auto Loan Payment', amount: '-385.00', day: 3, category: CATEGORY.carPayment })
const ELECTRIC = series({ n: 4, kind: 'bill', description: 'SPRINGFIELD ELECTRIC AUTOPAY', displayName: 'Springfield Electric', amount: '-118.00', day: 18, category: CATEGORY.utilities })
const WATER = series({ n: 5, kind: 'bill', description: 'SPRINGFIELD WATER UTIL', displayName: 'Springfield Water', amount: '-55.00', day: 21, category: CATEGORY.utilities })
const INTERNET = series({ n: 6, kind: 'bill', description: 'EXAMPLE FIBER INTERNET', displayName: 'Example Fiber', amount: '-70.00', day: 22, account: ACCOUNT.card, category: CATEGORY.internet })
const MOBILE = series({ n: 7, kind: 'bill', description: 'EXAMPLE MOBILE WIRELESS', displayName: 'Example Mobile', amount: '-85.00', day: 24, account: ACCOUNT.card, category: CATEGORY.phone })
const STREAMING = series({ n: 8, kind: 'subscription', description: 'SAMPLE STREAMING SVC', displayName: 'Sample Streaming', amount: '-15.00', day: 20, account: ACCOUNT.card, category: CATEGORY.streaming })
const MUSIC = series({ n: 9, kind: 'subscription', description: 'EXAMPLE MUSIC SUBSCR', displayName: 'Example Music', amount: '-11.00', day: 8, account: ACCOUNT.card, category: CATEGORY.streaming })
const GYM = series({ n: 10, kind: 'subscription', description: 'EXAMPLE FITNESS CLUB', displayName: 'Example Fitness Club', amount: '-40.00', day: 5, account: ACCOUNT.cashback, category: CATEGORY.gym })
const INSURANCE = series({ n: 11, kind: 'bill', description: 'EXAMPLE MUTUAL INS PREM', displayName: 'Example Mutual Insurance', amount: '-540.00', day: 12, category: CATEGORY.autoInsurance, recurrence: { alias: 'EVERY_QUARTER', frequency: 'MONTHLY', interval: 3, by_month_day: [12], by_day: [], by_month: [] }, perYear: 4 })
const CARD_PAYMENT = series({ n: 12, kind: 'credit_card_payment', description: 'Sample Rewards Visa', amount: '-1100.00', day: 25, category: CATEGORY.cardPayment })
const SAVINGS = series({ n: 13, kind: 'transfer', description: 'Transfer to High-Yield Savings', amount: '-500.00', day: 16, category: CATEGORY.transfer })
const REFUND = series({ n: 14, kind: 'refund', description: 'EXAMPLE OUTFITTERS RETURN', displayName: 'Example Outfitters return', amount: '48.00', day: 23, account: ACCOUNT.card, category: CATEGORY.clothing, recurrence: { alias: 'ONE_TIME', frequency: '', interval: 0, by_month_day: [], by_day: [], by_month: [] }, perYear: 1 })

const SERIES = [PAYCHECK, MORTGAGE, AUTO_LOAN, ELECTRIC, WATER, INTERNET, MOBILE, STREAMING, MUSIC, GYM, INSURANCE, CARD_PAYMENT, SAVINGS, REFUND]

type Status = 'upcoming' | 'past_due' | 'paid' | 'received' | 'skipped'

function link(label: string, subaccount: string, autopay: boolean) {
  return { connection_id: id('biller', label.length), biller: 'email-only', connection_label: label, subaccount_label: subaccount, health: 'ok', autopay }
}

function occurrence(one: ReturnType<typeof series>, dueOn: string, extra: object = {}) {
  const paid = dueOn <= TODAY_ISO
  const status: Status = paid ? (Number(one.amount) > 0 ? 'received' : 'paid') : 'upcoming'
  const settled = paid ? TRANSACTIONS.find((txn) => txn.date === dueOn && txn.statement_name.startsWith(one.description.slice(0, 12))) : undefined
  return {
    series_id: one.id,
    account_id: one.account_id,
    category_id: one.category_id,
    kind: one.kind,
    label: one.label,
    due_on: dueOn,
    amount: settled?.amount ?? one.amount,
    status,
    pays_on: dueOn,
    bill: null,
    bill_link: null,
    transaction_id: settled?.id ?? null,
    ...extra,
  }
}

/** The pay-manually reminder: a provider's statement that no autopay takes, with no series. */
const MEDICAL_BILL = {
  series_id: null,
  account_id: null,
  category_id: null,
  kind: 'bill',
  label: 'Example Medical Group',
  due_on: '2026-06-19',
  amount: '-85.00',
  status: 'upcoming',
  pays_on: null,
  bill: {
    id: id('bill', 2),
    amount_due: '85.00',
    due_on: '2026-06-19',
    status: 'open',
    source: 'email',
    fetched_at: '2026-06-09T06:00:00Z',
    document_id: id('doc', 9),
  },
  bill_link: link('Example Medical Group', 'Patient account ending 0001', false),
  transaction_id: null,
}

const ELECTRIC_BILL = {
  amount: '-118.00',
  pays_on: '2026-06-18',
  bill: { id: id('bill', 1), amount_due: '118.00', due_on: '2026-06-18', status: 'open', source: 'email', fetched_at: '2026-06-08T06:00:00Z', document_id: null },
  bill_link: link('Springfield Electric', 'Service account ending 0000', true),
}

const OCCURRENCES = [
  occurrence(PAYCHECK, '2026-06-01'),
  occurrence(MORTGAGE, '2026-06-01'),
  occurrence(AUTO_LOAN, '2026-06-03'),
  occurrence(GYM, '2026-06-05'),
  occurrence(MUSIC, '2026-06-08'),
  occurrence(PAYCHECK, '2026-06-15'),
  occurrence(SAVINGS, '2026-06-16'),
  occurrence(ELECTRIC, '2026-06-18', ELECTRIC_BILL),
  MEDICAL_BILL,
  occurrence(STREAMING, '2026-06-20'),
  occurrence(WATER, '2026-06-21'),
  occurrence(INTERNET, '2026-06-22'),
  occurrence(REFUND, '2026-06-23'),
  occurrence(MOBILE, '2026-06-24'),
  occurrence(CARD_PAYMENT, '2026-06-25'),
  occurrence(PAYCHECK, '2026-07-01'),
  occurrence(MORTGAGE, '2026-07-01'),
  occurrence(AUTO_LOAN, '2026-07-03'),
  occurrence(GYM, '2026-07-05'),
  occurrence(MUSIC, '2026-07-08'),
  occurrence(INSURANCE, '2026-07-12'),
  occurrence(PAYCHECK, '2026-07-15'),
]

function occurrenceList(query: URLSearchParams) {
  const from = query.get('from') ?? '2026-06-01'
  const to = query.get('to') ?? '2026-07-31'
  const items = OCCURRENCES.filter((one) => one.due_on >= from && one.due_on <= to)
  const income = sum(items.filter((one) => Number(one.amount) > 0).map((one) => one.amount))
  const expenses = sum(items.filter((one) => Number(one.amount) < 0).map((one) => one.amount))
  return {
    window: { from, to, date_field: 'effective' },
    items,
    summary: {
      income,
      expenses,
      net: sum([income, expenses]),
      count: items.length,
      past_due: items.filter((one) => one.status === 'past_due').length,
    },
  }
}

/* ---- Cash flow ----------------------------------------------------------- */

function days(from: string, count: number): string[] {
  const start = Date.parse(`${from}T00:00:00Z`)
  return Array.from({ length: count }, (_, n) => new Date(start + n * 86_400_000).toISOString().slice(0, 10))
}

const CASH_FLOW_DAYS = days(TODAY_ISO, 31)

function line(accountId: string, start: number, moves: Record<string, number>) {
  let balance = start
  const points = CASH_FLOW_DAYS.map((on) => {
    balance += moves[on] ?? 0
    return { on, balance: balance.toFixed(2) }
  })
  const lowest = points.reduce((low, point) => (Number(point.balance) < Number(low.balance) ? point : low))
  return {
    account_id: accountId,
    name: accountName(accountId),
    starting_balance: start.toFixed(2),
    points,
    lowest,
    first_below: points.find((point) => Number(point.balance) < 1000) ?? null,
  }
}

const CASH_FLOW_LINES = [
  line(ACCOUNT.checking, 6420, {
    '2026-06-16': -500,
    '2026-06-18': -118,
    '2026-06-21': -55,
    '2026-06-25': -1100,
    '2026-06-27': -320,
    '2026-07-01': 3250 - 1850,
    '2026-07-03': -385,
    '2026-07-12': -540,
    '2026-07-15': 3250,
  }),
  line(ACCOUNT.savings, 18500, { '2026-06-16': 500, '2026-06-28': 58 }),
]

/* ---- Spending plan ------------------------------------------------------- */

const PLAN_MONTH = '2026-06'
const JUNE = TRANSACTIONS.filter((txn) => txn.date.startsWith(PLAN_MONTH))

function entry(txn: Transaction, group: string | null = null) {
  return {
    id: txn.id,
    txn_id: txn.id,
    series_id: null,
    name: txn.payee,
    due_on: txn.date,
    status: Number(txn.amount) > 0 ? 'received' : 'paid',
    category_name: txn.splits.length > 0 ? `${txn.splits.length} categories` : categoryName(txn.category_id),
    amount: txn.amount,
    account_id: txn.account_id,
    is_split: txn.splits.length > 0,
    group,
    is_transfer: false,
    is_padding: false,
  }
}

function bucket(key: string, amount: string, contributing: ReturnType<typeof entry>[]) {
  return {
    key,
    calculated_amount: amount,
    effective_amount: amount,
    overwritten_amount: null,
    contributing,
    excluded: [],
    contributing_txn_ids: contributing.map((one) => one.txn_id),
    excluded_entry_ids: [],
  }
}

const PLAN_SERIES = [MORTGAGE, AUTO_LOAN, GYM, MUSIC, ELECTRIC, STREAMING, WATER, INTERNET, MOBILE]

function envelope(n: number, name: string, categoryIds: string[], target: number) {
  const rows = JUNE.filter((txn) => isSpending(txn) && txn.category_id !== null && categoryIds.includes(txn.category_id))
  const spent = rows.reduce((total, txn) => total - Number(txn.amount), 0)
  const pct = Math.round((spent / target) * 100)
  return {
    id: id('env', n),
    name,
    filter_id: id('filter', 40 + n),
    categories: categoryIds.map((categoryId) => ({ id: categoryId, name: categoryName(categoryId) })),
    target_amount: money(target),
    overwritten_target_amount: null,
    target: money(target),
    rollover_amount: '0.00',
    spent: money(spent),
    budget: money(target),
    available: money(target - spent),
    pct_used: String(pct),
    bar_pct: String(Math.min(pct, 100)),
    state: spent > target ? 'overspent' : 'normal',
    auto_release_rollover: false,
    recurring: true,
    txn_ids: rows.map((txn) => txn.id),
    entries: rows.map((txn) => entry(txn)),
  }
}

const ENVELOPES = [
  envelope(1, 'Dining out', [CATEGORY.dining], 350),
  envelope(2, 'Coffee', [CATEGORY.coffee], 50),
  envelope(3, 'Summer trip', [CATEGORY.travel], 800),
]

function spendingPlan() {
  const payroll = JUNE.filter((txn) => txn.category_id === CATEGORY.paycheck)
  const income = sum(payroll.map((txn) => txn.amount))
  const billRows = JUNE.filter((txn) => txn.is_bill || txn.is_subscription)
  const plannedBills = sum(PLAN_SERIES.map((one) => one.amount))
  const enveloped = new Set(ENVELOPES.flatMap((one) => one.txn_ids))
  const other = JUNE.filter((txn) => isSpending(txn) && !txn.is_bill && !txn.is_subscription && !enveloped.has(txn.id))
  const otherTotal = sum(other.map((txn) => txn.amount))
  const planned = money(-ENVELOPES.reduce((total, one) => total + Math.max(Number(one.target), Number(one.spent)), 0))
  const goals = '-400.00'
  const rollover = '140.00'
  const monthResult = sum([income, plannedBills, planned, otherTotal, goals])
  const left = sum([monthResult, rollover])
  const otherSpent = Math.abs(Number(otherTotal))
  const projectedOther = otherSpent * 1.5

  interface Slice {
    id: string | null
    rows: Transaction[]
    spent: number
    children: Map<string, Transaction[]>
  }
  const slices = new Map<string, Slice>()
  for (const txn of other) {
    const parts = txn.splits.length > 0 ? txn.splits.map((split) => ({ category: split.category_id, amount: split.amount })) : [{ category: txn.category_id, amount: txn.amount }]
    for (const part of parts) {
      const top = parentOf(part.category) ?? part.category
      const key = top ?? 'uncategorized'
      const slice: Slice = slices.get(key) ?? { id: top, rows: [], spent: 0, children: new Map() }
      slice.rows.push(txn)
      slice.spent -= Number(part.amount)
      const leaf = part.category ?? 'uncategorized'
      slice.children.set(leaf, [...(slice.children.get(leaf) ?? []), txn])
      slices.set(key, slice)
    }
  }

  return {
    month: PLAN_MONTH,
    as_of: TODAY_ISO,
    is_closed_out: false,
    closed_out_at: null,
    buckets: [
      bucket('income', income, payroll.map((txn) => entry(txn))),
      bucket('bills', plannedBills, billRows.map((txn) => entry(txn, txn.is_subscription ? 'subscription' : 'bill'))),
      bucket('planned_spend', planned, ENVELOPES.flatMap((one) => one.entries)),
      bucket('other_spend', otherTotal, other.map((txn) => entry(txn))),
      bucket('goals', goals, []),
      bucket('rollover', rollover, []),
    ],
    bills: PLAN_SERIES.map((one) => {
      const due = `${PLAN_MONTH}-${one.next_due_on.slice(8)}`
      const paid = billRows.find((txn) => txn.statement_name.startsWith(one.description.slice(0, 12)))
      return {
        id: `${one.id}:${due}`,
        group: one.kind === 'subscription' ? 'subscription' : 'bill',
        series_id: one.id,
        name: one.label,
        due_on: due,
        amount: paid?.amount ?? one.amount,
        is_fulfilled: paid !== undefined,
        txn_ids: paid ? [paid.id] : [],
        is_excluded: false,
      }
    }),
    bill_subtotals: [
      { group: 'bill', amount: sum(PLAN_SERIES.filter((one) => one.kind === 'bill').map((one) => one.amount)) },
      { group: 'subscription', amount: sum(PLAN_SERIES.filter((one) => one.kind === 'subscription').map((one) => one.amount)) },
    ],
    envelopes: ENVELOPES,
    contested_txn_ids: {},
    other_spend_by_category: [...slices.values()]
      .sort((a, b) => b.spent - a.spent)
      .map((slice) => ({
        category_id: slice.id,
        category_name: categoryName(slice.id),
        spent: money(slice.spent),
        txn_ids: [...new Set(slice.rows.map((txn) => txn.id))],
        children:
          slice.children.size > 1
            ? [...slice.children.entries()].map(([leaf, rows]) => ({
                category_id: leaf === 'uncategorized' ? null : leaf,
                category_name: categoryName(leaf === 'uncategorized' ? null : leaf),
                spent: money(
                  rows.reduce((total, txn) => {
                    const part = txn.splits.find((split) => split.category_id === leaf)
                    return total - Number(part?.amount ?? txn.amount)
                  }, 0),
                ),
                txn_ids: rows.map((txn) => txn.id),
                children: [],
              }))
            : [],
      })),
    projection: { type: 'average_n_months', buffer: '0.00', window_months: 3, start_on: null, end_on: null },
    left_this_month: left,
    per_day: money(Number(left) / 15),
    days_remaining: 15,
    month_result: monthResult,
    month_result_per_day: money(Number(monthResult) / 15),
    days_elapsed: 15,
    other_spend_to_date: money(otherSpent),
    projected_other_spending: money(projectedOther),
    projected_left: money(Number(left) - (projectedOther - otherSpent)),
    projected_month_result: money(Number(monthResult) - (projectedOther - otherSpent)),
  }
}

/* ---- Goals and watchlists ------------------------------------------------ */

interface GoalSeed {
  n: number
  name: string
  emoji: string | null
  account: string
  target: number
  saved: number
  targetOn: string | null
  monthly: number
}

function goal(seed: GoalSeed) {
  const pct = (seed.saved / seed.target) * 100
  return {
    id: id('goal', seed.n),
    name: seed.name,
    emoji: seed.emoji,
    account_id: seed.account,
    account_name: accountName(seed.account),
    funding_account_ids: [ACCOUNT.checking],
    funding: [{ account_id: ACCOUNT.checking, account_name: 'Everyday Checking', saved: money(seed.saved) }],
    target_amount: money(seed.target),
    target_on: seed.targetOn,
    completed_on: null,
    closed_on: null,
    stage: 'saving',
    is_taken_from_plan: true,
    saved_so_far: money(seed.saved),
    withdrawn: '0.00',
    spent_on_goal: '0.00',
    spending_by_category: [],
    unassigned_withdrawn: '0.00',
    funded: money(seed.saved),
    contributed_this_month: money(seed.monthly),
    left_to_save: money(seed.target - seed.saved),
    monthly_needed: seed.targetOn ? money(seed.monthly) : null,
    months_to_target: seed.targetOn ? Math.ceil((seed.target - seed.saved) / seed.monthly) : null,
    target_has_passed: false,
    pct_complete: pct.toFixed(2),
    pct_funded: pct.toFixed(2),
    is_complete: false,
    is_funded: false,
    txn_ids: [],
    withdrawal_txn_ids: [],
    spending_txn_ids: [],
    contributions: [],
  }
}

const GOALS = [
  goal({ n: 1, name: 'Summer trip', emoji: '🏖️', account: ACCOUNT.savings, target: 3000, saved: 2400, targetOn: '2026-08-01', monthly: 400 }),
  goal({ n: 2, name: 'Emergency fund', emoji: '🛟', account: ACCOUNT.savings, target: 20000, saved: 12500, targetOn: null, monthly: 200 }),
  goal({ n: 3, name: 'New roof', emoji: '🏠', account: ACCOUNT.savings, target: 15000, saved: 4200, targetOn: '2027-09-01', monthly: 700 }),
  goal({ n: 4, name: 'Holiday gifts', emoji: '🎁', account: ACCOUNT.savings, target: 800, saved: 300, targetOn: '2026-12-01', monthly: 100 }),
]

function watchlist(n: number, name: string, emoji: string | null, target: string | null, spent: number, trend: number[]) {
  const limit = target === null ? null : Number(target)
  return {
    id: id('watch', n),
    name,
    emoji,
    filter_id: id('filter', 20 + n),
    period: 'month',
    this_month_spent: money(spent),
    month_projection: money(spent * 2),
    year_to_date: money(trend.reduce((total, one) => total + one, 0) + spent),
    monthly_trend: ['2026-01', '2026-02', '2026-03', '2026-04', '2026-05', '2026-06'].map((month, index) => ({
      month,
      spent: money(index === 5 ? spent : trend[index]),
      is_partial: index === 5,
    })),
    target_amount: target,
    left_to_target: limit === null ? null : money(limit - spent),
    pct_of_target: limit === null ? null : ((spent / limit) * 100).toFixed(2),
    is_over_target: limit !== null && spent > limit,
    is_projected_over_target: limit !== null && spent * 2 > limit,
    as_of: TODAY_ISO,
  }
}

const WATCHLISTS = [
  watchlist(1, 'Eating out', '🍜', '450.00', 186, [392, 418, 365, 441, 402]),
  watchlist(2, 'Coffee', '☕', '60.00', 34.5, [58, 52, 61, 55, 57]),
  watchlist(3, 'Amazon', '📦', null, 98, [112, 64, 141, 88, 120]),
]

/* ---- Rules and guidance -------------------------------------------------- */

interface ItemSeed {
  field: string
  operator: string
  position?: number
  value_texts?: string[]
  value_ids?: string[]
  text?: string
}

function item(n: number, seed: ItemSeed) {
  return {
    id: id('item', n),
    field: seed.field,
    operator: seed.operator,
    group_index: 0,
    position: seed.position ?? 0,
    negated: false,
    value_ids: seed.value_ids ?? [],
    value_texts: seed.value_texts ?? [],
    text: seed.text ?? null,
    amount_min: null,
    amount_max: null,
    date_from: null,
    date_to: null,
    date_preset: null,
    state: null,
  }
}

function ruleFilter(n: number, scope: string, items: ReturnType<typeof item>[]) {
  return { id: id('filter', n), name: null, scope, query_text: null, items }
}

export function ruleActions(set: object) {
  return {
    set_payee: null,
    set_category_id: null,
    add_tag_ids: [],
    set_notes: null,
    set_excluded_from_reports: null,
    set_excluded_from_spending_plan: null,
    set_is_reviewed: null,
    ...set,
  }
}

function rule(n: number, name: string, items: ReturnType<typeof item>[], set: object, active = true) {
  return {
    id: id('rule', n),
    name,
    filter_id: id('filter', 60 + n),
    filter: ruleFilter(60 + n, 'rule', items),
    owns_filter: true,
    priority: n,
    is_active: active,
    actions: ruleActions(set),
  }
}

const RULES = [
  rule(1, 'Payroll', [item(1, { field: 'text', operator: 'contains', text: 'EXAMPLE EMPLOYER PAYROLL' })], { set_payee: 'Example Employer Payroll', set_category_id: CATEGORY.paycheck }),
  rule(2, 'Corner Coffee', [item(2, { field: 'text', operator: 'contains', text: 'CORNER COFFEE' })], { set_payee: 'Corner Coffee', set_category_id: CATEGORY.coffee }),
  rule(3, 'Pharmacy is HSA eligible', [item(3, { field: 'payee', operator: 'in', value_texts: ['Example Pharmacy'] })], { set_category_id: CATEGORY.pharmacy, add_tag_ids: [TAG.hsa] }),
  rule(4, 'Streaming services', [item(4, { field: 'text', operator: 'contains', text: 'STREAMING' })], { set_category_id: CATEGORY.streaming }),
  rule(5, 'Card payments stay out of spending', [item(5, { field: 'text', operator: 'contains', text: 'CARD PAYMENT' })], { set_category_id: CATEGORY.cardPayment, set_excluded_from_spending_plan: true }),
  rule(6, 'School lunch account', [item(6, { field: 'text', operator: 'contains', text: 'SCHOOL DIST' })], { set_payee: 'School Lunch Account', set_category_id: CATEGORY.kids }),
]

function rulePreview() {
  const rows = TRANSACTIONS.filter((txn) => txn.statement_name.includes('CORNER COFFEE')).slice(0, 6)
  return {
    rule_id: id('rule', 2),
    matched: rows.length,
    changed: 1,
    unchanged: rows.length - 1,
    truncated: false,
    since: null,
    changes: rows.slice(0, 1).map((txn) => ({
      transaction_id: txn.id,
      date: txn.date,
      account_name: accountName(txn.account_id),
      statement_name: txn.statement_name,
      payee: txn.payee,
      amount: txn.amount,
      actions: ruleActions({ set_payee: 'Corner Coffee', set_category_id: CATEGORY.coffee }),
    })),
  }
}

function retirement() {
  const years = []
  let balance = 174700
  for (let year = 2026; year <= 2071; year += 1) {
    const age = year - 1985
    const working = age < 65
    const contributed = working ? 14400 : 0
    const drawn = working ? 0 : 72000
    const growth = Math.round(balance * 0.055)
    balance = Math.max(balance + contributed + growth - drawn, 0)
    const deflator = 1.025 ** (year - 2026)
    years.push({
      year,
      age,
      balance: balance.toFixed(2),
      balance_in_todays_dollars: (balance / deflator).toFixed(2),
      contributed: contributed.toFixed(2),
      drawn: drawn.toFixed(2),
      growth: growth.toFixed(2),
      high_balance: (balance * 1.2).toFixed(2),
      low_balance: (balance * 0.8).toFixed(2),
      high_balance_in_todays_dollars: ((balance * 1.2) / deflator).toFixed(2),
      low_balance_in_todays_dollars: ((balance * 0.8) / deflator).toFixed(2),
    })
  }
  const atRetirement = years.find((one) => one.age === 65) ?? years[0]
  return {
    assumptions: {
      start_year: 2026,
      current_age: 41,
      retirement_age: 65,
      current_balance: '174700.00',
      is_balance_from_accounts: true,
      monthly_contribution: '1200.00',
      annual_return: '0.055',
      annual_inflation: '0.025',
      withdrawal_rate: '0.04',
      target_annual_income: '72000.00',
      life_expectancy: 90,
      annual_living_expenses: '72000.00',
      annual_retirement_income: '24000.00',
      pre_retirement_tax_rate: '0.2',
      post_retirement_tax_rate: '0.15',
      return_spread: '0.02',
      advanced: null,
    },
    years,
    years_to_retirement: 24,
    balance_at_retirement: atRetirement.balance,
    balance_at_retirement_in_todays_dollars: atRetirement.balance_in_todays_dollars,
    total_contributed: '345600.00',
    total_growth: '780000.00',
    annual_income: '76000.00',
    annual_income_in_todays_dollars: '41500.00',
    meets_target: true,
    shortfall: '0.00',
    runs_out_at_age: null,
  }
}

export const PLANNING = {
  'GET /occurrences': occurrenceList,
  'GET /cash-flow': (query: URLSearchParams) => ({
    window: { from: query.get('from'), to: query.get('to'), date_field: 'effective' },
    threshold: query.get('threshold') ?? '1000.00',
    accounts: CASH_FLOW_LINES,
    combined: CASH_FLOW_DAYS.map((on, n) => ({
      on,
      balance: CASH_FLOW_LINES.reduce((total, one) => total + Number(one.points[n].balance), 0).toFixed(2),
    })),
    occurrences: OCCURRENCES.filter((one) => one.due_on >= TODAY_ISO),
  }),
  'GET /cash-flow-forecast': { available: false, unavailable: 'No forecast has been written for these accounts yet.' },
  'GET /series': SERIES,
  'GET /series/suggested': (query: URLSearchParams) =>
    query.get('dismissed') === 'true'
      ? []
      : [
          {
            signature: 'demo-bakery',
            account_id: ACCOUNT.cashback,
            category_id: CATEGORY.dining,
            kind: 'subscription',
            description: 'SAMPLE MEAL KIT',
            display_name: 'Sample Meal Kit',
            label: 'Sample Meal Kit',
            amount: '-60.00',
            currency: 'USD',
            recurrence: { alias: 'EVERY_MONTH', ...MONTHLY, by_month_day: [9] },
            start_on: '2026-07-09',
            occurrences: 4,
            first_seen: '2026-03-09',
            last_seen: '2026-06-09',
            confidence: 0.9,
            match_criteria: 'auto',
            match_amount_min: null,
            match_amount_max: null,
            transaction_ids: [byNumber(SHOWCASE.games).id],
          },
        ],
  'GET /series/refunds': {
    expected: [{ series: REFUND, expected_on: '2026-06-23', settled_on: null, transaction_id: null }],
    completed: [],
  },
  'GET /spending-plan/:month': spendingPlan(),
  'GET /goals': GOALS,
  'GET /goals/:id/suggestions': { kind: 'spending', from: '2026-05-01', to: '2026-08-01', truncated: false, rows: [] },
  'GET /planning/retirement': retirement(),
  'GET /watchlists': WATCHLISTS,
  'GET /watchlists/:id': (query: URLSearchParams) => ({
    ...WATCHLISTS[0],
    month: query.get('month') ?? PLAN_MONTH,
    spent: '186.00',
    by_category: [{ key: CATEGORY.dining, label: 'Restaurants', spent: '186.00', share: '1' }],
    by_payee: [
      { key: 'Sample Bistro', label: 'Sample Bistro', spent: '96.00', share: '0.52' },
      { key: 'Example Pizza Co.', label: 'Example Pizza Co.', spent: '90.00', share: '0.48' },
    ],
    by_tag: [],
  }),
  'GET /rules': RULES,
  'GET /rules/:id/preview': rulePreview(),
  'GET /guidance': [
    {
      id: id('guidance', 1),
      name: 'Warehouse club trips',
      instruction: 'Split Costco receipts by what the order lists: food under Groceries, cleaning and paper goods under Household Supplies.',
      filter_id: id('filter', 11),
      filter: ruleFilter(11, 'guidance', [item(11, { field: 'payee', operator: 'in', value_texts: ['Costco'] })]),
      owns_filter: true,
      applies_to: 'Transactions whose payee is Costco.',
      is_active: true,
      position: 1,
    },
    {
      id: id('guidance', 2),
      name: 'Medical spending',
      instruction: 'Anything paid from the HSA is medical: file it under Health & Fitness and tag it HSA eligible.',
      filter_id: id('filter', 12),
      filter: ruleFilter(12, 'guidance', [item(12, { field: 'account', operator: 'in', value_ids: [ACCOUNT.hsa] })]),
      owns_filter: true,
      applies_to: 'Transactions on Health Savings (HSA).',
      is_active: true,
      position: 2,
    },
  ],
}
