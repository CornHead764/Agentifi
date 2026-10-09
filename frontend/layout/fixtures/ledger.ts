/** The register, its summaries, net worth and the spending plan. */

import { ACCOUNT, ACCOUNTS, CATEGORIES, CATEGORY, TAG, id } from './core'

interface TxnSeed {
  n: number
  date: string
  account: string
  amount: string
  payee: string
  category: string | null
  pending?: boolean
  reviewed?: boolean
  tags?: string[]
  splits?: { amount: string; category: string; memo: string | null }[]
  notes?: string
  memo?: string
  /** Paid by payroll deduction: the row carries the mark after its payee. */
  padded?: boolean
  /** On the card that requires receipts: where the row stands on one. */
  receipt?: 'missing' | 'on_file' | 'not_needed'
}

function transaction(seed: TxnSeed) {
  const txnId = id('txn', seed.n)
  return {
    id: txnId,
    account_id: seed.account,
    date: seed.date,
    effective_date: null,
    amount: seed.amount,
    currency: 'USD',
    amount_primary: null,
    fx_rate_used: null,
    statement_name: seed.payee.toUpperCase(),
    payee: seed.payee,
    memo: seed.memo ?? '',
    transacted_on: null,
    notes: seed.notes ?? null,
    check_number: null,
    category_id: seed.splits ? null : seed.category,
    source: 'sync',
    is_pending: seed.pending ?? false,
    is_reviewed: seed.reviewed ?? true,
    excluded_from_reports: false,
    excluded_from_spending_plan: false,
    is_bill: false,
    is_subscription: false,
    transfer_pair_id: null,
    padded_txn_id: null,
    padding_txn_id: seed.padded ? id('txn', 900 + seed.n) : null,
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
    attachment_count: 0,
    receipt_status: seed.receipt ?? null,
    receipt_not_needed: seed.receipt === 'not_needed',
    suggestion: null,
    checking_category: false,
    category_checked_at: null,
    category_check_note: '',
    category_check_run_id: null,
  }
}

export const TRANSACTIONS = [
  transaction({ n: 1, date: '2026-06-15', account: ACCOUNT.card, amount: '-12.00', payee: 'Example Coffee Bar', category: CATEGORY.dining, pending: true, reviewed: false, receipt: 'missing' }),
  transaction({ n: 2, date: '2026-06-14', account: ACCOUNT.card, amount: '-150.00', payee: 'The Extremely Long Sample Payee Name Incorporated Of Example Town', category: CATEGORY.shopping, reviewed: false, padded: true }),
  transaction({ n: 3, date: '2026-06-14', account: ACCOUNT.checking, amount: '-80.00', payee: 'Corner Grocery', category: CATEGORY.groceries, tags: [TAG.vacation, TAG.work] }),
  transaction({
    n: 4,
    date: '2026-06-13',
    account: ACCOUNT.card,
    amount: '-200.00',
    payee: 'Sample Superstore',
    category: null,
    splits: [
      { amount: '-120.00', category: CATEGORY.groceries, memo: 'Food' },
      { amount: '-80.00', category: CATEGORY.shopping, memo: 'Household' },
    ],
  }),
  transaction({ n: 5, date: '2026-06-12', account: ACCOUNT.checking, amount: '-100.00', payee: 'City Power & Light', category: CATEGORY.utilities, notes: 'Paid online' }),
  transaction({ n: 6, date: '2026-06-12', account: ACCOUNT.checking, amount: '-1500.00', payee: 'Home Mortgage Payment', category: CATEGORY.rent }),
  transaction({ n: 7, date: '2026-06-11', account: ACCOUNT.card, amount: '-40.00', payee: 'Example Fuel Stop', category: CATEGORY.fuel, reviewed: false, receipt: 'missing' }),
  transaction({ n: 8, date: '2026-06-10', account: ACCOUNT.card, amount: '-15.00', payee: 'Sample Streaming Service', category: CATEGORY.streaming }),
  transaction({ n: 9, date: '2026-06-10', account: ACCOUNT.checking, amount: '-60.00', payee: 'Corner Grocery', category: CATEGORY.groceries }),
  transaction({ n: 10, date: '2026-06-09', account: ACCOUNT.card, amount: '-30.00', payee: 'Example Pizza Place', category: CATEGORY.dining, memo: 'ONLINE ORDER' }),
  transaction({ n: 11, date: '2026-06-08', account: ACCOUNT.checking, amount: '-500.00', payee: 'Payment to Sample Rewards Card', category: CATEGORY.cardPayment }),
  transaction({ n: 12, date: '2026-06-08', account: ACCOUNT.card, amount: '500.00', payee: 'Payment Received, Thank You', category: CATEGORY.cardPayment }),
  transaction({ n: 13, date: '2026-06-05', account: ACCOUNT.checking, amount: '3000.00', payee: 'Example Employer Payroll', category: CATEGORY.paycheck }),
  transaction({ n: 14, date: '2026-06-04', account: ACCOUNT.card, amount: '-25.00', payee: 'Example Bookshop', category: CATEGORY.shopping }),
  transaction({ n: 15, date: '2026-06-03', account: ACCOUNT.card, amount: '-45.00', payee: 'Example Coffee Bar', category: CATEGORY.dining }),
  transaction({ n: 16, date: '2026-06-02', account: ACCOUNT.checking, amount: '-70.00', payee: 'Corner Grocery', category: CATEGORY.groceries }),
  transaction({ n: 17, date: '2026-06-01', account: ACCOUNT.savings, amount: '10.00', payee: 'Interest Paid', category: CATEGORY.interest }),
  transaction({ n: 18, date: '2026-05-30', account: ACCOUNT.card, amount: '-35.00', payee: 'Example Fuel Stop', category: CATEGORY.fuel }),
  transaction({ n: 19, date: '2026-05-28', account: ACCOUNT.card, amount: '-20.00', payee: 'Unknown Sample Merchant', category: null, reviewed: false }),
  transaction({ n: 20, date: '2026-05-20', account: ACCOUNT.checking, amount: '3000.00', payee: 'Example Employer Payroll', category: CATEGORY.paycheck }),
]

function inWindow(date: string, query: URLSearchParams): boolean {
  const from = query.get('from')
  const to = query.get('to')
  return (!from || date >= from) && (!to || date <= to)
}

function sum(amounts: readonly string[]): string {
  const cents = amounts.reduce((total, amount) => total + Math.round(Number(amount) * 100), 0)
  const sign = cents < 0 ? '-' : ''
  const whole = Math.abs(cents)
  return `${sign}${Math.floor(whole / 100)}.${String(whole % 100).padStart(2, '0')}`
}

function selectTransactions(query: URLSearchParams) {
  const accounts = query.getAll('account_id').filter(Boolean)
  const reviewed = query.get('reviewed')
  return TRANSACTIONS.filter(
    (txn) =>
      inWindow(txn.date, query) &&
      (accounts.length === 0 || accounts.includes(txn.account_id)) &&
      (reviewed === null || String(txn.is_reviewed) === reviewed),
  )
}

function transactionPage(query: URLSearchParams) {
  const rows = selectTransactions(query)
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

function categoryName(categoryId: string | null): string {
  return CATEGORIES.find((category) => category.id === categoryId)?.name ?? 'Uncategorized'
}

function aggregate(query: URLSearchParams) {
  const direction = query.get('direction') === 'income' ? 'income' : 'spending'
  const groupBy = query.get('group_by') ?? 'category'
  const rows = selectTransactions(query).filter((txn) =>
    direction === 'income' ? Number(txn.amount) > 0 : Number(txn.amount) < 0,
  )
  const keyOf = (txn: (typeof rows)[number]) => {
    if (groupBy === 'payee') return { key: txn.payee, label: txn.payee }
    if (groupBy === 'none') return { key: 'all', label: 'All' }
    if (groupBy === 'tag') return { key: txn.tag_ids[0] ?? 'untagged', label: txn.tag_ids[0] ? 'Vacation' : 'Untagged' }
    return { key: txn.category_id ?? 'uncategorized', label: categoryName(txn.category_id) }
  }
  const bucketsOf = (subset: typeof rows) => {
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
    months: months.map((month) => ({
      month,
      buckets: bucketsOf(rows.filter((txn) => txn.date.startsWith(month))),
    })),
  }
}

function accountSummary(query: URLSearchParams) {
  const balance = ACCOUNTS[0].balances
  return {
    account_id: ACCOUNT.checking,
    window: { from: query.get('from'), to: query.get('to'), date_field: query.get('date_field') ?? 'posted' },
    opening_balance: '4000.00',
    ending_balance: balance.balance,
    total: '1000.00',
    count: 8,
    balances: balance,
  }
}

/* ---- Net worth ----------------------------------------------------------- */

const KINDS = ['cash', 'credit_card', 'loan', 'investment', 'asset'] as const

function netWorthPoint(on: string, step: number) {
  const cash = 15000 + step * 400
  const investment = 38000 + step * 400
  const asset = 300000
  const card = 800
  const loan = 202000 - step * 400
  const assets = cash + investment + asset
  const debt = card + loan
  const money = (value: number) => value.toFixed(2)
  const amounts: Record<(typeof KINDS)[number], number> = {
    cash,
    credit_card: card,
    loan,
    investment,
    asset,
  }
  return {
    on,
    assets: money(assets),
    debt: money(debt),
    net: money(assets - debt),
    by_kind: KINDS.map((kind) => ({ kind, amount: money(amounts[kind]) })),
    equity: money(asset - loan),
  }
}

const NET_WORTH_POINTS = ['2025-12-31', '2026-01-31', '2026-02-28', '2026-03-31', '2026-04-30', '2026-05-31', '2026-06-15'].map(
  (on, step) => netWorthPoint(on, step - 1),
)

/** The move from start to end as a percentage of the start's magnitude, as the API sends it. */
function percent(start: string, end: string): string | null {
  if (Number(start) === 0) return null
  return (((Number(end) - Number(start)) / Math.abs(Number(start))) * 100).toFixed(2)
}

function netWorthAccount(accountId: string, name: string, kind: string, type: string, start: string, end: string) {
  return {
    account_id: accountId,
    name,
    kind,
    type,
    is_closed: false,
    start,
    end,
    change: sum([end, `-${start}`.replace('--', '')]),
    change_pct: percent(start, end),
  }
}

function group(kind: string, label: string, side: 'asset' | 'debt', accounts: ReturnType<typeof netWorthAccount>[]) {
  const start = sum(accounts.map((account) => account.start))
  const end = sum(accounts.map((account) => account.end))
  return {
    kind,
    class: label,
    side,
    account_count: accounts.length,
    start,
    end,
    change: sum([end, `-${start}`.replace('--', '')]),
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
    change: sum([end.net, `-${start.net}`]),
    change_pct: percent(start.net, end.net),
    debt_to_asset: (Number(end.debt) / Number(end.assets)).toFixed(4),
    groups: [
      group('cash', 'Cash', 'asset', [
        netWorthAccount(ACCOUNT.checking, 'Everyday Checking', 'cash', 'checking', '4000.00', '5000.00'),
        netWorthAccount(ACCOUNT.savings, 'Rainy Day Savings', 'cash', 'savings', '10600.00', '12000.00'),
      ]),
      group('investment', 'Investments', 'asset', [
        netWorthAccount(ACCOUNT.brokerage, 'Example Brokerage', 'investment', 'brokerage', '37600.00', '40000.00'),
      ]),
      group('asset', 'Property', 'asset', [
        netWorthAccount(ACCOUNT.house, 'Home', 'asset', 'real_estate', '300000.00', '300000.00'),
      ]),
      group('credit_card', 'Credit cards', 'debt', [
        netWorthAccount(ACCOUNT.card, 'Sample Rewards Card', 'credit_card', 'credit_card', '800.00', '800.00'),
      ]),
      group('loan', 'Loans', 'debt', [
        netWorthAccount(ACCOUNT.mortgage, 'Home Mortgage', 'loan', 'mortgage', '202400.00', '200000.00'),
      ]),
    ],
    included_accounts: 6,
    total_accounts: 6,
    unconverted_currencies: [],
  }
}

/* ---- Spending plan ------------------------------------------------------- */

function entry(txn: (typeof TRANSACTIONS)[number], group: string | null = null) {
  return {
    id: txn.id,
    txn_id: txn.id,
    series_id: null,
    name: txn.payee,
    due_on: txn.date,
    status: 'paid',
    category_name: categoryName(txn.category_id),
    amount: txn.amount,
    account_id: txn.account_id,
    is_split: txn.splits.length > 0,
    group,
    is_transfer: false,
    is_padding: false,
  }
}

/** Income recorded beside a purchase paid by payroll deduction; the plan folds these into one line. */
function padding(n: number, amount: string) {
  return {
    ...entry(byNumber(13)),
    id: id('txn', 900 + n),
    txn_id: id('txn', 900 + n),
    name: 'Example Canteen (paycheck deduction)',
    amount,
    status: 'received',
    is_padding: true,
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

const byNumber = (n: number) => TRANSACTIONS[n - 1]

/** Many small categories, so the Other Spend bubbles spread past one screen's width. */
const MINOR_OTHER_SPEND: readonly (readonly [number, string, string])[] = [
  [60, 'Pets', '30.00'],
  [61, 'Gifts', '25.00'],
  [62, 'Hobbies', '22.00'],
  [63, 'Books', '18.00'],
  [64, 'Parking', '15.00'],
  [65, 'Coffee', '12.00'],
  [66, 'Laundry', '10.00'],
  [67, 'Postage', '8.00'],
  [68, 'Donations', '6.00'],
  [69, 'Fees', '5.00'],
  [70, 'Haircuts', '4.00'],
  [71, 'Tolls', '3.00'],
]

function spendingPlan(month: string) {
  const payroll = byNumber(13)
  const bills = [byNumber(5), byNumber(6), byNumber(8)]
  const dining = [byNumber(1), byNumber(10), byNumber(15)]
  const other = [byNumber(2), byNumber(3), byNumber(7), byNumber(9), byNumber(14), byNumber(16)]
  return {
    month,
    as_of: '2026-06-15',
    is_closed_out: false,
    closed_out_at: null,
    buckets: [
      bucket('income', '3014.50', [entry(payroll), padding(1, '6.00'), padding(2, '8.50')]),
      bucket('bills', '-1615.00', bills.map((txn) => entry(txn, 'bill'))),
      bucket('planned_spend', '-300.00', dining.map((txn) => entry(txn))),
      bucket('other_spend', '-485.00', other.map((txn) => entry(txn))),
      bucket('goals', '-200.00', []),
      bucket('rollover', '100.00', []),
    ],
    bills: [
      {
        id: `${id('series', 1)}:2026-06-12`,
        group: 'bill',
        series_id: id('series', 1),
        name: 'City Power & Light',
        due_on: '2026-06-12',
        amount: '-100.00',
        is_fulfilled: true,
        txn_ids: [byNumber(5).id],
        is_excluded: false,
      },
      {
        id: `${id('series', 2)}:2026-06-12`,
        group: 'bill',
        series_id: id('series', 2),
        name: 'Home Mortgage Payment',
        due_on: '2026-06-12',
        amount: '-1500.00',
        is_fulfilled: true,
        txn_ids: [byNumber(6).id],
        is_excluded: false,
      },
      {
        id: `${id('series', 3)}:2026-06-10`,
        group: 'subscription',
        series_id: id('series', 3),
        name: 'Sample Streaming Service',
        due_on: '2026-06-10',
        amount: '-15.00',
        is_fulfilled: true,
        txn_ids: [byNumber(8).id],
        is_excluded: false,
      },
      {
        id: `${id('series', 4)}:2026-06-25`,
        group: 'bill',
        series_id: id('series', 4),
        name: 'Example Phone Company',
        due_on: '2026-06-25',
        amount: '-50.00',
        is_fulfilled: false,
        txn_ids: [],
        is_excluded: false,
      },
    ],
    bill_subtotals: [
      { group: 'bill', amount: '-1650.00' },
      { group: 'subscription', amount: '-15.00' },
    ],
    envelopes: [
      {
        id: id('env', 1),
        name: 'Dining out',
        filter_id: id('filter', 1),
        categories: [{ id: CATEGORY.dining, name: 'Restaurants' }],
        target_amount: '300.00',
        overwritten_target_amount: null,
        target: '300.00',
        rollover_amount: '0.00',
        spent: '87.00',
        budget: '300.00',
        available: '213.00',
        pct_used: '29',
        bar_pct: '29',
        state: 'normal',
        auto_release_rollover: false,
        recurring: true,
        txn_ids: dining.map((txn) => txn.id),
        entries: dining.map((txn) => entry(txn)),
      },
      {
        id: id('env', 2),
        name: 'Weekend trips and other things that make a long envelope name',
        filter_id: id('filter', 2),
        categories: [{ id: CATEGORY.fuel, name: 'Gas & Fuel' }],
        target_amount: '50.00',
        overwritten_target_amount: null,
        target: '50.00',
        rollover_amount: '0.00',
        spent: '75.00',
        budget: '50.00',
        available: '-25.00',
        pct_used: '150',
        bar_pct: '100',
        state: 'overspent',
        auto_release_rollover: false,
        recurring: true,
        txn_ids: [byNumber(7).id, byNumber(18).id],
        entries: [entry(byNumber(7))],
      },
    ],
    contested_txn_ids: {},
    other_spend_by_category: [
      {
        category_id: CATEGORY.food,
        category_name: 'Food & Dining',
        spent: '210.00',
        txn_ids: [byNumber(3).id, byNumber(9).id, byNumber(16).id],
        children: [
          { category_id: CATEGORY.groceries, category_name: 'Groceries', spent: '210.00', txn_ids: [byNumber(3).id, byNumber(9).id, byNumber(16).id], children: [] },
        ],
      },
      { category_id: CATEGORY.shopping, category_name: 'Shopping', spent: '175.00', txn_ids: [byNumber(2).id, byNumber(14).id], children: [] },
      { category_id: CATEGORY.fuel, category_name: 'Gas & Fuel', spent: '40.00', txn_ids: [byNumber(7).id], children: [] },
      ...MINOR_OTHER_SPEND.map(([n, name, spent]) => ({
        category_id: id('cat', n),
        category_name: name,
        spent,
        txn_ids: [],
        children: [],
      })),
    ],
    projection: { type: 'run_rate', buffer: '0.00', window_months: 3, start_on: null, end_on: null },
    left_this_month: '500.00',
    per_day: '33.33',
    days_remaining: 15,
    month_result: '400.00',
    month_result_per_day: '26.67',
    days_elapsed: 15,
    other_spend_to_date: '485.00',
    projected_other_spending: '970.00',
    projected_left: '15.00',
    projected_month_result: '-85.00',
  }
}

export const LEDGER = {
  'GET /transactions': transactionPage,
  'GET /transactions/aggregate': aggregate,
  'GET /transactions/payees': [...new Set(TRANSACTIONS.map((txn) => txn.payee))],
  'GET /transactions/category-checks': { total: 0, done: 0, rows: [] },
  // A finished request, so the strip and its comparison are laid out.
  'GET /category-suggestion-batches/latest': {
    batch: {
      id: id('batch', 1),
      created_at: '2026-09-30T10:00:00Z',
      cancelled: false,
      dismissed: false,
      rows: 1312,
      done: 1312,
      pending: 0,
      not_run: 0,
      finished: true,
      reviewed: { agreed: 1104, differs: 37, unsure: 12, suggested: 0, undetermined: 0, skipped: 41, failed: 0 },
      unreviewed: { agreed: 71, differs: 18, unsure: 4, suggested: 16, undetermined: 7, skipped: 2, failed: 0 },
    },
  },
  'GET /transactions/:id': TRANSACTIONS[0],
  'GET /documents': [],
  'GET /bill-payments/transactions/:id': [],
  // No purchase behind the row: the answer the panel expects for one.
  'GET /merchants/transactions/:id': { status: 404, body: { detail: 'No purchase' } },
  'GET /accounts/:id/summary': accountSummary,
  'POST /filters': (_query: URLSearchParams, body: unknown) => ({
    id: id('filter', 99),
    ...(typeof body === 'object' && body !== null ? body : {}),
  }),
  'GET /filters': [
    {
      id: id('filter', 2),
      name: 'Coffee runs',
      scope: 'saved_view',
      query_text: null,
      position: 0,
      items: [
        {
          id: id('filter-item', 1),
          field: 'payee',
          operator: 'in',
          group_index: 0,
          position: 0,
          negated: false,
          value_ids: [],
          value_texts: ['Example Coffee Bar'],
          text: null,
          amount_min: null,
          amount_max: null,
          date_from: null,
          date_to: null,
          date_preset: null,
          state: null,
        },
      ],
    },
    {
      id: id('filter', 3),
      name: 'A saved view whose name runs long enough to wrap in the menu',
      scope: 'saved_view',
      query_text: null,
      position: 1,
      items: [
        {
          id: id('filter-item', 2),
          field: 'is_reviewed',
          operator: 'is_true',
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
          state: true,
        },
      ],
    },
  ],
  'GET /filters/:id': {
    id: id('filter', 1),
    name: null,
    scope: 'ad_hoc',
    query_text: null,
    items: [],
  },
  'GET /spaces/current/dashboard': { layout: null },
  'GET /assistant-automations/pending': [],
  'GET /net-worth': netWorth,
  'GET /spending-plan/:month': spendingPlan('2026-06'),
}
