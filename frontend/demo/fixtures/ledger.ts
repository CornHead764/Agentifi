/**
 * The register, its summaries and net worth: half a year of the Sample
 * household's invented history, generated from a fixed seed so every run
 * draws the same rows.
 */

import { ACCOUNT, ACCOUNTS, CATEGORY, TAG, accountName, categoryName, id, parentOf } from './core'
import { state, type FilterItem } from './state'

/* ---- Rows ---------------------------------------------------------------- */

interface TxnSeed {
  n: number
  date: string
  account: string
  amount: string
  /** The bank's wording; the clean payee is what the register shows. */
  statement: string
  payee: string
  category: string | null
  pending?: boolean
  reviewed?: boolean
  tags?: string[]
  splits?: { amount: string; category: string; memo: string | null }[]
  notes?: string
  receipt?: 'missing' | 'on_file'
  bill?: boolean
  subscription?: boolean
  transfer?: boolean
}

interface Suggestion {
  action_id: string
  conversation_id: string
  run_id: string | null
  tool: string
  summary: string
  category_id: string | null
  splits: { amount: string; category_id: string | null; memo: string }[]
  created_at: string
}

/** A field that starts empty and a scene may fill. */
function nothing<T>(): T | null {
  return null
}

function transaction(seed: TxnSeed) {
  const txnId = id('txn', seed.n)
  const onFile = seed.receipt === 'on_file'
  return {
    id: txnId,
    account_id: seed.account,
    date: seed.date,
    effective_date: null,
    amount: seed.amount,
    currency: 'USD',
    amount_primary: null,
    fx_rate_used: null,
    statement_name: seed.statement,
    payee: seed.payee,
    memo: '',
    transacted_on: null,
    notes: seed.notes ?? null,
    check_number: null,
    category_id: seed.splits ? null : seed.category,
    source: 'sync',
    is_pending: seed.pending ?? false,
    is_reviewed: seed.reviewed ?? true,
    excluded_from_reports: false,
    excluded_from_spending_plan: false,
    is_bill: seed.bill ?? false,
    is_subscription: seed.subscription ?? false,
    transfer_pair_id: seed.transfer ? id('pair', seed.n) : null,
    padded_txn_id: null,
    padding_txn_id: null,
    user_flag: null,
    user_flag_note: null,
    series_id: null,
    series_due_on: null,
    balance: null,
    splits: (seed.splits ?? []).map((split, index) => ({
      id: id('split', seed.n * 10 + index),
      position: index,
      amount: split.amount,
      category_id: split.category,
      memo: split.memo,
      tag_ids: [],
    })),
    matched_split_ids: null,
    matched_amount: null,
    tag_ids: seed.tags ?? [],
    attachment_count: onFile ? 1 : 0,
    receipt_status: seed.receipt ?? null,
    receipt_not_needed: false,
    suggestion: nothing<Suggestion>(),
    checking_category: false,
    category_checked_at: nothing<string>(),
    category_check_note: '',
    category_check_run_id: nothing<string>(),
  }
}

export type Transaction = ReturnType<typeof transaction>

/** A seeded generator, so the history is the same on every run. */
function seeded(seed: number) {
  let value = seed
  return () => {
    value = (value + 0x6d2b79f5) | 0
    let mixed = Math.imul(value ^ (value >>> 15), 1 | value)
    mixed = (mixed + Math.imul(mixed ^ (mixed >>> 7), 61 | mixed)) ^ mixed
    return ((mixed ^ (mixed >>> 14)) >>> 0) / 4294967296
  }
}

const random = seeded(2026)

function between(low: number, high: number): number {
  return Math.round(low + random() * (high - low))
}

export function money(value: number): string {
  return value.toFixed(2)
}

export const TODAY_ISO = '2026-06-15'
export const MONTHS = ['2026-01', '2026-02', '2026-03', '2026-04', '2026-05', '2026-06']

/** The rows the scenes point at, by number. */
export const SHOWCASE = {
  costco: 1,
  amazon: 2,
  pharmacy: 3,
  clinic: 4,
  bakery: 5,
  hardware: 6,
  noodles: 7,
  games: 8,
  parking: 9,
  coffee: 10,
}

export const COSTCO_SPLITS = [
  { amount: '-125.00', category: CATEGORY.groceries, memo: 'Produce, eggs, salmon, coffee and pantry' },
  { amount: '-59.00', category: CATEGORY.household, memo: 'Paper towels, detergent, trash bags' },
  { amount: '-32.00', category: CATEGORY.personal, memo: 'Toothpaste and sunscreen' },
  { amount: '-23.00', category: CATEGORY.school, memo: 'Markers and construction paper' },
]

export const AMAZON_SPLITS = [
  { amount: '-38.00', category: CATEGORY.electronics, memo: 'USB-C cable and wireless mouse' },
  { amount: '-15.00', category: CATEGORY.books, memo: 'The Sample Novel (paperback)' },
  { amount: '-9.00', category: CATEGORY.household, memo: 'Kitchen sponges' },
]

const SHOWCASE_ROWS: TxnSeed[] = [
  { n: SHOWCASE.costco, date: '2026-06-13', account: ACCOUNT.card, amount: '-239.00', statement: 'COSTCO WHSE #0000 SPRINGFIELD', payee: 'Costco', category: null, splits: COSTCO_SPLITS },
  { n: SHOWCASE.amazon, date: '2026-06-11', account: ACCOUNT.card, amount: '-62.00', statement: 'AMAZON MKTPL*AB0CD0EF0', payee: 'Amazon', category: null, splits: AMAZON_SPLITS },
  { n: SHOWCASE.pharmacy, date: '2026-06-12', account: ACCOUNT.hsa, amount: '-25.00', statement: 'EXAMPLE PHARMACY #0100', payee: 'Example Pharmacy', category: CATEGORY.pharmacy, receipt: 'missing', tags: [TAG.hsa] },
  { n: SHOWCASE.clinic, date: '2026-06-08', account: ACCOUNT.hsa, amount: '-45.00', statement: 'EXAMPLE FAMILY CLINIC', payee: 'Example Family Clinic', category: CATEGORY.doctor, receipt: 'missing', tags: [TAG.hsa] },
  { n: SHOWCASE.bakery, date: '2026-06-14', account: ACCOUNT.cashback, amount: '-14.50', statement: 'SQ *SAMPLE BAKERY', payee: 'Sample Bakery', category: null, reviewed: false },
  { n: SHOWCASE.hardware, date: '2026-06-13', account: ACCOUNT.card, amount: '-36.00', statement: 'EXAMPLE HARDWARE #0012', payee: 'Example Hardware', category: null, reviewed: false },
  { n: SHOWCASE.noodles, date: '2026-06-12', account: ACCOUNT.card, amount: '-43.00', statement: 'TST* SAMPLE NOODLE BAR', payee: 'Sample Noodle Bar', category: null, reviewed: false },
  { n: SHOWCASE.games, date: '2026-06-10', account: ACCOUNT.cashback, amount: '-20.00', statement: 'PAYPAL *EXAMPLEGAMES', payee: 'Example Games', category: null, reviewed: false },
  { n: SHOWCASE.parking, date: '2026-06-09', account: ACCOUNT.cashback, amount: '-12.00', statement: 'SAMPLE CITY PARKING 004', payee: 'Sample City Parking', category: null, reviewed: false },
  { n: SHOWCASE.coffee, date: '2026-06-15', account: ACCOUNT.cashback, amount: '-5.50', statement: 'CORNER COFFEE CO', payee: 'Corner Coffee', category: CATEGORY.coffee, pending: true, reviewed: false },
]

/** What the category check proposes for each uncategorized row. */
export const SUGGESTED: Record<number, { category: string; summary: string }> = {
  [SHOWCASE.bakery]: { category: CATEGORY.dining, summary: 'A bakery counter purchase; filed like your other cafés under Restaurants.' },
  [SHOWCASE.hardware]: { category: CATEGORY.homeImprovement, summary: 'Example Hardware is a home improvement store; earlier visits are filed there.' },
  [SHOWCASE.noodles]: { category: CATEGORY.dining, summary: 'A restaurant charge (TST* is a restaurant point of sale).' },
  [SHOWCASE.games]: { category: CATEGORY.hobbies, summary: 'A game purchase through PayPal; Hobbies matches past ones.' },
  [SHOWCASE.parking]: { category: CATEGORY.auto, summary: 'City parking garage: Auto & Transport.' },
}

function generate(): TxnSeed[] {
  const rows: TxnSeed[] = []
  let n = 100
  const add = (seed: Omit<TxnSeed, 'n'>) => {
    if (seed.date > TODAY_ISO) return
    n += 1
    rows.push({ n, ...seed })
  }
  const restaurants = ['Sample Bistro', 'Example Pizza Co.', 'Taco Example', 'Sample Noodle House']
  const amazonCategories = [CATEGORY.household, CATEGORY.electronics, CATEGORY.books, CATEGORY.clothing]

  MONTHS.forEach((month, index) => {
    const on = (day: number) => `${month}-${String(day).padStart(2, '0')}`
    const recent = (day: number) => on(day) >= '2026-06-11'

    add({ date: on(1), account: ACCOUNT.checking, amount: '3250.00', statement: 'EXAMPLE EMPLOYER PAYROLL DIR DEP', payee: 'Example Employer Payroll', category: CATEGORY.paycheck })
    add({ date: on(15), account: ACCOUNT.checking, amount: '3250.00', statement: 'EXAMPLE EMPLOYER PAYROLL DIR DEP', payee: 'Example Employer Payroll', category: CATEGORY.paycheck, reviewed: !recent(15) })
    add({ date: on(1), account: ACCOUNT.hsa, amount: '150.00', statement: 'HSA CONTRIBUTION PAYROLL', payee: 'HSA Contribution', category: CATEGORY.transfer })
    add({ date: on(1), account: ACCOUNT.checking, amount: '-1850.00', statement: 'SAMPLE CU MORTGAGE PMT', payee: 'Mortgage Payment', category: CATEGORY.mortgage, bill: true })
    add({ date: on(2), account: ACCOUNT.checking, amount: '-40.00', statement: 'EXAMPLE SCHOOL DIST LUNCH', payee: 'School Lunch Account', category: CATEGORY.kids })
    add({ date: on(3), account: ACCOUNT.checking, amount: '-385.00', statement: 'SAMPLE CU AUTO LOAN PMT', payee: 'Auto Loan Payment', category: CATEGORY.carPayment, bill: true })
    add({ date: on(5), account: ACCOUNT.cashback, amount: '-40.00', statement: 'EXAMPLE FITNESS CLUB', payee: 'Example Fitness Club', category: CATEGORY.gym, subscription: true, reviewed: index < 5 })
    add({ date: on(8), account: ACCOUNT.card, amount: '-11.00', statement: 'EXAMPLE MUSIC SUBSCR', payee: 'Example Music', category: CATEGORY.streaming, subscription: true, reviewed: index < 5 })
    add({ date: on(12), account: ACCOUNT.cashback, amount: '-28.00', statement: 'EXAMPLE BARBER SHOP', payee: 'Example Barber Shop', category: CATEGORY.personal })
    add({ date: on(16), account: ACCOUNT.checking, amount: '-500.00', statement: 'TRANSFER TO SAVINGS 2222', payee: 'Transfer to High-Yield Savings', category: CATEGORY.transfer, transfer: true })
    add({ date: on(16), account: ACCOUNT.savings, amount: '500.00', statement: 'TRANSFER FROM CHECKING 1111', payee: 'Transfer from Everyday Checking', category: CATEGORY.transfer, transfer: true })
    add({ date: on(18), account: ACCOUNT.checking, amount: money(-between(92, 141)), statement: 'SPRINGFIELD ELECTRIC AUTOPAY', payee: 'Springfield Electric', category: CATEGORY.utilities, bill: true })
    add({ date: on(20), account: ACCOUNT.card, amount: '-15.00', statement: 'SAMPLE STREAMING SVC', payee: 'Sample Streaming', category: CATEGORY.streaming, subscription: true })
    add({ date: on(21), account: ACCOUNT.checking, amount: money(-between(46, 63)), statement: 'SPRINGFIELD WATER UTIL', payee: 'Springfield Water', category: CATEGORY.utilities, bill: true })
    add({ date: on(22), account: ACCOUNT.card, amount: '-70.00', statement: 'EXAMPLE FIBER INTERNET', payee: 'Example Fiber', category: CATEGORY.internet, bill: true })
    add({ date: on(24), account: ACCOUNT.card, amount: '-85.00', statement: 'EXAMPLE MOBILE WIRELESS', payee: 'Example Mobile', category: CATEGORY.phone, bill: true })
    const cardPayment = money(between(950, 1350))
    add({ date: on(25), account: ACCOUNT.checking, amount: `-${cardPayment}`, statement: 'EXAMPLE BANK CARD PAYMENT', payee: 'Payment to Sample Rewards Visa', category: CATEGORY.cardPayment, transfer: true })
    add({ date: on(25), account: ACCOUNT.card, amount: cardPayment, statement: 'PAYMENT RECEIVED THANK YOU', payee: 'Payment Received', category: CATEGORY.cardPayment, transfer: true })
    const cashbackPayment = money(between(240, 380))
    add({ date: on(27), account: ACCOUNT.checking, amount: `-${cashbackPayment}`, statement: 'SAMPLE CU CARD PAYMENT', payee: 'Payment to Cash Back Card', category: CATEGORY.cardPayment, transfer: true })
    add({ date: on(27), account: ACCOUNT.cashback, amount: cashbackPayment, statement: 'PAYMENT THANK YOU', payee: 'Payment Received', category: CATEGORY.cardPayment, transfer: true })
    add({ date: on(28), account: ACCOUNT.savings, amount: money(between(54, 63)), statement: 'INTEREST PAID', payee: 'Interest Paid', category: CATEGORY.interest })
    if (index === 0 || index === 3) {
      add({ date: on(12), account: ACCOUNT.checking, amount: '-540.00', statement: 'EXAMPLE MUTUAL INS PREM', payee: 'Example Mutual Insurance', category: CATEGORY.autoInsurance, bill: true })
    }

    for (const day of [2, 9, 16, 23, 30]) {
      add({ date: on(day), account: ACCOUNT.card, amount: money(-between(84, 172)), statement: 'EXAMPLE MARKET #0042', payee: 'Example Market', category: CATEGORY.groceries, reviewed: !recent(day) })
    }
    for (const day of [12, 26]) {
      add({ date: on(day), account: ACCOUNT.cashback, amount: money(-between(24, 58)), statement: 'SAMPLE MARKET CO-OP', payee: 'Sample Market Co-op', category: CATEGORY.groceries, reviewed: !recent(day) })
    }
    if (index < 5) {
      add({ date: on(index % 2 === 0 ? 6 : 13), account: ACCOUNT.card, amount: money(-between(178, 262)), statement: 'COSTCO WHSE #0000 SPRINGFIELD', payee: 'Costco', category: CATEGORY.groceries })
    }
    for (const day of index === 5 ? [4] : [4, 19]) {
      add({ date: on(day), account: ACCOUNT.card, amount: money(-between(18, 94)), statement: 'AMAZON MKTPL*AB0CD0EF0', payee: 'Amazon', category: amazonCategories[(index + day) % amazonCategories.length] })
    }
    for (const day of [7, 17, 27]) {
      add({ date: on(day), account: ACCOUNT.cashback, amount: money(-between(38, 58)), statement: 'EXAMPLE FUEL #0007', payee: 'Example Fuel', category: CATEGORY.fuel })
    }
    for (const day of [3, 10, 11, 17, 24]) {
      add({ date: on(day), account: ACCOUNT.cashback, amount: money(-between(5, 7)), statement: 'CORNER COFFEE CO', payee: 'Corner Coffee', category: CATEGORY.coffee, reviewed: !recent(day) })
    }
    ;[6, 14, 21, 28].forEach((day, slot) => {
      const name = restaurants[(index + slot) % 4]
      add({ date: on(day), account: ACCOUNT.card, amount: money(-between(24, 86)), statement: name.toUpperCase(), payee: name, category: CATEGORY.dining, reviewed: !recent(day) })
    })
    add({ date: on(11), account: ACCOUNT.hsa, amount: money(-between(12, 38)), statement: 'EXAMPLE PHARMACY #0100', payee: 'Example Pharmacy', category: CATEGORY.pharmacy, receipt: 'on_file', tags: [TAG.hsa] })
    if (index % 2 === 0) {
      add({ date: on(19), account: ACCOUNT.hsa, amount: money(-between(35, 60)), statement: 'EXAMPLE FAMILY CLINIC', payee: 'Example Family Clinic', category: CATEGORY.doctor, receipt: 'on_file', tags: [TAG.hsa] })
    }
    if (index % 2 === 1) {
      add({ date: on(13), account: ACCOUNT.card, amount: money(-between(45, 120)), statement: 'EXAMPLE OUTFITTERS', payee: 'Example Outfitters', category: CATEGORY.clothing })
    }
  })

  add({ date: '2026-02-10', account: ACCOUNT.card, amount: '-65.00', statement: 'EXAMPLE FLORIST', payee: 'Example Florist', category: CATEGORY.gifts })
  add({ date: '2026-05-09', account: ACCOUNT.card, amount: '-80.00', statement: 'EXAMPLE FLORIST', payee: 'Example Florist', category: CATEGORY.gifts })
  add({ date: '2026-03-04', account: ACCOUNT.hsa, amount: '-120.00', statement: 'SAMPLE DENTAL CARE', payee: 'Sample Dental Care', category: CATEGORY.dental, receipt: 'on_file', tags: [TAG.hsa] })
  add({ date: '2026-03-07', account: ACCOUNT.card, amount: '-54.00', statement: 'SAMPLE HOBBY SHOP', payee: 'Sample Hobby Shop', category: CATEGORY.hobbies })
  add({ date: '2026-05-09', account: ACCOUNT.card, amount: '-38.50', statement: 'SAMPLE HOBBY SHOP', payee: 'Sample Hobby Shop', category: CATEGORY.hobbies })
  add({ date: '2026-03-21', account: ACCOUNT.card, amount: '-142.00', statement: 'EXAMPLE HARDWARE #0012', payee: 'Example Hardware', category: CATEGORY.homeImprovement })
  add({ date: '2026-05-16', account: ACCOUNT.card, amount: '-86.00', statement: 'EXAMPLE HARDWARE #0012', payee: 'Example Hardware', category: CATEGORY.homeImprovement })
  add({ date: '2026-06-02', account: ACCOUNT.card, amount: '-480.00', statement: 'EXAMPLE AIRLINES 0000000', payee: 'Example Airlines', category: CATEGORY.travel, tags: [TAG.vacation] })
  add({ date: '2026-06-05', account: ACCOUNT.card, amount: '-300.00', statement: 'SAMPLE BEACH RENTALS', payee: 'Sample Beach Rentals', category: CATEGORY.travel, tags: [TAG.vacation] })
  return rows
}

export const TRANSACTIONS: Transaction[] = [...SHOWCASE_ROWS, ...generate()]
  .map(transaction)
  .sort((a, b) => (a.date === b.date ? Number(a.amount) - Number(b.amount) : b.date.localeCompare(a.date)))

export function byNumber(n: number): Transaction {
  const found = TRANSACTIONS.find((txn) => txn.id === id('txn', n))
  if (!found) throw new Error(`no demo transaction ${n}`)
  return found
}

/* ---- What a scene has done to a row ------------------------------------- */

const CHECK_MS = 1800

function numberOf(someId: string): number {
  return Number(someId.slice(-12))
}

export function suggestionAction(n: number): string {
  return id('action', 500 + n)
}

/** The row number behind a suggestion's action id, or null for any other action. */
export function suggestionNumber(actionId: string): number | null {
  const n = numberOf(actionId) - 500
  return actionId === suggestionAction(n) && SUGGESTED[n] ? n : null
}

function suggestionFor(n: number): Suggestion | null {
  const proposal = SUGGESTED[n]
  if (!proposal) return null
  return {
    action_id: suggestionAction(n),
    conversation_id: id('conv', 9),
    run_id: id('run', 1),
    tool: 'update_transaction',
    summary: proposal.summary,
    category_id: proposal.category,
    splits: [],
    created_at: '2026-06-15T15:00:00Z',
  }
}

/** A row as the server would answer it now, after this scene's clicks. */
export function view(txn: Transaction): Transaction {
  const n = numberOf(txn.id)
  if (state.uploads.has(txn.id)) {
    return { ...txn, attachment_count: 1, receipt_status: 'on_file' }
  }
  if (state.applied.has(n)) {
    return { ...txn, category_id: SUGGESTED[n].category, is_reviewed: true, suggestion: null }
  }
  const asked = state.checks.get(txn.id)
  if (asked !== undefined) {
    const checking = Date.now() - asked < CHECK_MS
    return {
      ...txn,
      checking_category: checking,
      suggestion: checking ? null : suggestionFor(n),
      category_checked_at: checking ? null : '2026-06-15T15:00:00Z',
      category_check_run_id: checking ? null : id('run', 1),
    }
  }
  return txn
}

function categoryChecks(query: URLSearchParams) {
  const rows = query.getAll('id').map((txnId) => {
    const found = TRANSACTIONS.find((one) => one.id === txnId)
    const txn = found ? view(found) : null
    return {
      transaction_id: txnId,
      checking: txn?.checking_category ?? false,
      category_id: txn?.category_id ?? null,
      category_checked_at: txn?.category_checked_at ?? null,
      category_check_note: '',
      category_check_run_id: txn?.category_check_run_id ?? null,
      suggestion: txn?.suggestion ?? null,
    }
  })
  return { total: rows.length, done: rows.filter((row) => !row.checking).length, rows }
}

/* ---- The register -------------------------------------------------------- */

function inWindow(date: string, query: URLSearchParams): boolean {
  const from = query.get('from')
  const to = query.get('to')
  return (!from || date >= from) && (!to || date <= to)
}

export function sum(amounts: readonly string[]): string {
  const cents = amounts.reduce((total, amount) => total + Math.round(Number(amount) * 100), 0)
  const sign = cents < 0 ? '-' : ''
  const whole = Math.abs(cents)
  return `${sign}${Math.floor(whole / 100)}.${String(whole % 100).padStart(2, '0')}`
}

function categoriesOf(txn: Transaction): string[] {
  const own = txn.splits.length > 0 ? txn.splits.map((split) => split.category_id ?? '') : [txn.category_id ?? '']
  return own.flatMap((categoryId) => [categoryId, parentOf(categoryId) ?? ''])
}

function itemMatches(txn: Transaction, item: FilterItem): boolean {
  const ids = item.value_ids ?? []
  const texts = (item.value_texts ?? []).map((text) => text.toLowerCase())
  const wanted = item.state ?? true
  const words = `${txn.payee} ${txn.statement_name} ${txn.notes ?? ''}`.toLowerCase()
  switch (item.field) {
    case 'is_missing_receipt':
      return (txn.receipt_status === 'missing') === wanted
    case 'is_uncategorized':
      return (txn.category_id === null && txn.splits.length === 0) === wanted
    case 'is_bill_or_subscription':
      return (txn.is_bill || txn.is_subscription) === wanted
    case 'is_reviewed':
      return txn.is_reviewed === wanted
    case 'is_pending':
      return txn.is_pending === wanted
    case 'has_attachment':
      return txn.attachment_count > 0 === wanted
    case 'has_category_suggestion':
      return (txn.suggestion !== null) === wanted
    case 'category':
      return categoriesOf(txn).some((categoryId) => ids.includes(categoryId))
    case 'account':
      return ids.includes(txn.account_id)
    case 'tag':
      return txn.tag_ids.some((tagId) => ids.includes(tagId))
    case 'payee':
      return item.operator === 'contains'
        ? texts.some((text) => txn.payee.toLowerCase().includes(text))
        : texts.includes(txn.payee.toLowerCase())
    case 'text':
    case 'statement_name':
      return words.includes((item.text ?? '').toLowerCase())
    default:
      return true
  }
}

function filterMatches(txn: Transaction, filterId: string | null): boolean {
  if (!filterId) return true
  const filter = state.filters.get(filterId)
  if (!filter) return true
  const text = filter.query_text?.trim().toLowerCase()
  if (text && !text.includes(':') && !`${txn.payee} ${txn.statement_name}`.toLowerCase().includes(text)) return false
  return filter.items.every((item) => itemMatches(txn, item) !== (item.negated ?? false))
}

export function selectTransactions(query: URLSearchParams): Transaction[] {
  const accounts = query.getAll('account_id').filter(Boolean)
  const reviewed = query.get('reviewed')
  const filterId = query.get('filter_id')
  const search = query.get('q')?.toLowerCase()
  return TRANSACTIONS.map(view).filter(
    (txn) =>
      inWindow(txn.date, query) &&
      (accounts.length === 0 || accounts.includes(txn.account_id)) &&
      (reviewed === null || String(txn.is_reviewed) === reviewed) &&
      (!search || `${txn.payee} ${txn.statement_name}`.toLowerCase().includes(search)) &&
      filterMatches(txn, filterId),
  )
}

function transactionPage(query: URLSearchParams) {
  const rows = selectTransactions(query)
  if (query.get('order') === 'asc') rows.reverse()
  const limit = Number(query.get('limit') ?? 200)
  const offset = Number(query.get('offset') ?? 0)
  const total = sum(rows.map((txn) => txn.amount))
  return {
    items: rows.slice(offset, offset + limit),
    count: rows.length,
    total,
    full_total: total,
    partial_count: 0,
    padding_count: 0,
    padding_total: '0.00',
    window: { from: query.get('from'), to: query.get('to'), date_field: query.get('date_field') ?? 'posted' },
    limit,
    offset,
  }
}

export function isSpending(txn: Transaction): boolean {
  return !txn.transfer_pair_id && txn.category_id !== CATEGORY.transfer && Number(txn.amount) < 0
}

function aggregate(query: URLSearchParams) {
  const direction = query.get('direction') === 'income' ? 'income' : 'spending'
  const groupBy = query.get('group_by') ?? 'category'
  const rows = selectTransactions(query).filter((txn) =>
    direction === 'income'
      ? !txn.transfer_pair_id && txn.category_id !== CATEGORY.transfer && Number(txn.amount) > 0
      : isSpending(txn),
  )
  const keyOf = (txn: Transaction) => {
    if (groupBy === 'payee') return { key: txn.payee, label: txn.payee }
    if (groupBy === 'none') return { key: 'all', label: 'All' }
    if (groupBy === 'account') return { key: txn.account_id, label: accountName(txn.account_id) }
    const top = parentOf(txn.category_id) ?? txn.category_id
    return { key: top ?? 'uncategorized', label: categoryName(top) }
  }
  const bucketsOf = (subset: Transaction[]) => {
    const totals = new Map<string, { key: string; label: string; amounts: string[] }>()
    for (const txn of subset) {
      const { key, label } = keyOf(txn)
      const entry = totals.get(key) ?? { key, label, amounts: [] }
      entry.amounts.push(String(Math.abs(Number(txn.amount))))
      totals.set(key, entry)
    }
    return [...totals.values()]
      .map((entry) => ({ key: entry.key, label: entry.label, total: sum(entry.amounts) }))
      .sort((a, b) => Number(b.total) - Number(a.total))
  }
  const months = [...new Set(rows.map((txn) => txn.date.slice(0, 7)))].sort()
  return {
    direction,
    group_by: groupBy,
    total: sum(rows.map((txn) => String(Math.abs(Number(txn.amount))))),
    count: rows.length,
    buckets: bucketsOf(rows),
    months: months.map((month) => ({ month, buckets: bucketsOf(rows.filter((txn) => txn.date.startsWith(month))) })),
  }
}

function accountSummary(query: URLSearchParams, _body: unknown, path: string) {
  const accountId = path.split('/')[2]
  const account = ACCOUNTS.find((one) => one.id === accountId) ?? ACCOUNTS[0]
  const scoped = new URLSearchParams(query)
  scoped.delete('account_id')
  scoped.append('account_id', account.id)
  const rows = selectTransactions(scoped)
  const moved = sum(rows.map((txn) => txn.amount))
  return {
    account_id: account.id,
    window: { from: query.get('from'), to: query.get('to'), date_field: query.get('date_field') ?? 'posted' },
    opening_balance: money(Number(account.balances.balance) - Number(moved)),
    ending_balance: account.balances.balance,
    total: moved,
    count: rows.length,
    balances: account.balances,
  }
}

function saveFilter(_query: URLSearchParams, body: unknown) {
  const filterId = id('filter', 900 + state.nextFilter)
  state.nextFilter += 1
  const draft = typeof body === 'object' && body !== null ? body : {}
  const items: FilterItem[] = 'items' in draft && Array.isArray(draft.items) ? draft.items : []
  const queryText = 'query_text' in draft && typeof draft.query_text === 'string' ? draft.query_text : null
  state.filters.set(filterId, { query_text: queryText, items })
  return { name: null, scope: 'ad_hoc', ...draft, id: filterId, query_text: queryText, items }
}

/* ---- Net worth ----------------------------------------------------------- */

const KINDS = ['cash', 'credit_card', 'loan', 'investment', 'asset'] as const

const NET_WORTH_DATES = ['2025-12-31', '2026-01-31', '2026-02-28', '2026-03-31', '2026-04-30', '2026-05-31', '2026-06-15']
const SERIES_BY_KIND: Record<(typeof KINDS)[number], number[]> = {
  cash: [25200, 25900, 26450, 27300, 27900, 28800, 29770],
  investment: [160400, 163100, 161800, 166200, 169900, 172300, 174700],
  asset: [439200, 439000, 438800, 438600, 438400, 438200, 438000],
  credit_card: [1450, 1720, 1380, 1590, 1510, 1690, 1625],
  loan: [281700, 281050, 280400, 279700, 279000, 278300, 277600],
}

const NET_WORTH_POINTS = NET_WORTH_DATES.map((on, step) => {
  const at = (kind: (typeof KINDS)[number]) => SERIES_BY_KIND[kind][step]
  const assets = at('cash') + at('investment') + at('asset')
  const debt = at('credit_card') + at('loan')
  return {
    on,
    assets: money(assets),
    debt: money(debt),
    net: money(assets - debt),
    by_kind: KINDS.map((kind) => ({ kind, amount: money(at(kind)) })),
    equity: money(at('asset') - at('loan')),
  }
})

function percent(start: string, end: string): string | null {
  if (Number(start) === 0) return null
  return (((Number(end) - Number(start)) / Math.abs(Number(start))) * 100).toFixed(2)
}

function change(start: string, end: string): string {
  return money(Number(end) - Number(start))
}

function netWorthAccount(accountId: string, start: string) {
  const account = ACCOUNTS.find((one) => one.id === accountId) ?? ACCOUNTS[0]
  const end = money(Math.abs(Number(account.balances.balance)))
  return {
    account_id: accountId,
    name: account.name,
    kind: account.kind,
    type: account.type,
    is_closed: false,
    start,
    end,
    change: change(start, end),
    change_pct: percent(start, end),
  }
}

function group(kind: string, label: string, side: 'asset' | 'debt', accounts: ReturnType<typeof netWorthAccount>[]) {
  const start = sum(accounts.map((one) => one.start))
  const end = sum(accounts.map((one) => one.end))
  return {
    kind,
    class: label,
    side,
    account_count: accounts.length,
    start,
    end,
    change: change(start, end),
    change_pct: percent(start, end),
    accounts,
  }
}

function netWorth(query: URLSearchParams) {
  const start = NET_WORTH_POINTS[0]
  const end = NET_WORTH_POINTS[NET_WORTH_POINTS.length - 1]
  return {
    window: { from: query.get('from'), to: query.get('to'), date_field: 'posted' },
    granularity: 'month',
    points: NET_WORTH_POINTS,
    start,
    end,
    change: change(start.net, end.net),
    change_pct: percent(start.net, end.net),
    debt_to_asset: (Number(end.debt) / Number(end.assets)).toFixed(4),
    groups: [
      group('cash', 'Cash', 'asset', [
        netWorthAccount(ACCOUNT.checking, '5400.00'),
        netWorthAccount(ACCOUNT.savings, '15800.00'),
        netWorthAccount(ACCOUNT.hsa, '4000.00'),
      ]),
      group('investment', 'Investments', 'asset', [
        netWorthAccount(ACCOUNT.brokerage, '44100.00'),
        netWorthAccount(ACCOUNT.retirement, '116300.00'),
      ]),
      group('asset', 'Property', 'asset', [
        netWorthAccount(ACCOUNT.house, '420000.00'),
        netWorthAccount(ACCOUNT.car, '19200.00'),
      ]),
      group('credit_card', 'Credit cards', 'debt', [
        netWorthAccount(ACCOUNT.card, '1180.00'),
        netWorthAccount(ACCOUNT.cashback, '270.00'),
      ]),
      group('loan', 'Loans', 'debt', [
        netWorthAccount(ACCOUNT.mortgage, '270300.00'),
        netWorthAccount(ACCOUNT.autoLoan, '11400.00'),
      ]),
    ],
    included_accounts: ACCOUNTS.length,
    total_accounts: ACCOUNTS.length,
    unconverted_currencies: [],
  }
}

/* ---- Saved views --------------------------------------------------------- */

function savedView(n: number, name: string, item: FilterItem) {
  return {
    id: id('filter', n),
    name,
    scope: 'saved_view',
    query_text: null,
    position: n,
    items: [
      {
        id: id('filter-item', n),
        group_index: 0,
        position: 0,
        negated: false,
        value_ids: [],
        value_texts: [],
        text: null,
        amount_min: null,
        amount_max: null,
        date_from: null,
        date_to: null,
        date_preset: null,
        state: null,
        ...item,
      },
    ],
  }
}

export const LEDGER = {
  'GET /transactions': transactionPage,
  'GET /transactions/aggregate': aggregate,
  'GET /transactions/payees': [...new Set(TRANSACTIONS.map((txn) => txn.payee))].sort(),
  'GET /transactions/category-checks': categoryChecks,
  'GET /transactions/:id': (_query: URLSearchParams, _body: unknown, path: string) =>
    view(TRANSACTIONS.find((txn) => txn.id === path.split('/')[2]) ?? TRANSACTIONS[0]),
  'GET /bill-payments/transactions/:id': [],
  'GET /accounts/:id/summary': accountSummary,
  'POST /filters': saveFilter,
  'GET /filters': [
    savedView(1, 'Summer trip', { field: 'tag', operator: 'in', value_ids: [TAG.vacation] }),
    savedView(2, 'HSA eligible', { field: 'tag', operator: 'in', value_ids: [TAG.hsa] }),
  ],
  'GET /filters/:id': (_query: URLSearchParams, _body: unknown, path: string) => {
    const filterId = path.split('/')[2]
    const saved = state.filters.get(filterId)
    return { id: filterId, name: null, scope: 'ad_hoc', query_text: saved?.query_text ?? null, items: saved?.items ?? [] }
  },
  'GET /net-worth': netWorth,
}
