/**
 * Investing, Reports and the Assistant. Invented: tickers that trade nowhere,
 * round figures, and dates around 2026-06-15.
 */

import { USER_ID } from './auth'
import { ACCOUNT, CATEGORY, id } from './core'

const SECURITY = {
  market: id('sec', 1),
  bond: id('sec', 2),
  international: id('sec', 3),
  placement: id('sec', 4),
}

const SECURITIES = [
  {
    id: SECURITY.market,
    symbol: 'SMPL',
    name: 'Sample Total Market Fund',
    kind: 'fund',
    exchange: 'NASDAQ',
    currency: 'USD',
    last_price: '200.00',
    prior_close: '199.00',
    last_price_at: '2026-06-15T15:00:00Z',
  },
  {
    id: SECURITY.bond,
    symbol: 'FAKE',
    name: 'Fake Bond Index Fund',
    kind: 'fund',
    exchange: 'NASDAQ',
    currency: 'USD',
    last_price: '50.00',
    prior_close: '50.10',
    last_price_at: '2026-06-15T15:00:00Z',
  },
  {
    id: SECURITY.international,
    symbol: 'DEMO',
    name: 'Demo International Developed Markets Stock Index Fund Admiral Shares',
    kind: 'fund',
    exchange: 'NASDAQ',
    currency: 'USD',
    last_price: '100.00',
    prior_close: '99.50',
    last_price_at: '2026-06-15T15:00:00Z',
  },
  {
    id: SECURITY.placement,
    symbol: 'XMPL',
    name: 'Example Private Placement',
    kind: 'other',
    exchange: null,
    currency: 'USD',
    last_price: '5000.00',
    prior_close: null,
    last_price_at: null,
  },
]

const HOLDINGS = [
  {
    id: id('hold', 1),
    account_id: ACCOUNT.brokerage,
    security_id: SECURITY.market,
    symbol: 'SMPL',
    name: 'Sample Total Market Fund',
    shares: '100',
    price: '200.00',
    currency: 'USD',
    market_value: '20000.00',
    is_unquoted: false,
    cost_basis: '15000.00',
    total_gain: '5000.00',
    total_gain_pct: '0.3333',
    is_cost_basis_complete: true,
    day_change: '100.00',
    day_change_pct: '0.0050',
    share: '0.5',
  },
  {
    id: id('hold', 2),
    account_id: ACCOUNT.brokerage,
    security_id: SECURITY.bond,
    symbol: 'FAKE',
    name: 'Fake Bond Index Fund',
    shares: '200',
    price: '50.00',
    currency: 'USD',
    market_value: '10000.00',
    is_unquoted: false,
    cost_basis: '10500.00',
    total_gain: '-500.00',
    total_gain_pct: '-0.0476',
    is_cost_basis_complete: true,
    day_change: '-20.00',
    day_change_pct: '-0.0020',
    share: '0.25',
  },
  {
    id: id('hold', 3),
    account_id: ACCOUNT.brokerage,
    security_id: SECURITY.international,
    symbol: 'DEMO',
    name: 'Demo International Developed Markets Stock Index Fund Admiral Shares',
    shares: '50',
    price: '100.00',
    currency: 'USD',
    market_value: '5000.00',
    is_unquoted: false,
    cost_basis: null,
    total_gain: null,
    total_gain_pct: null,
    is_cost_basis_complete: false,
    day_change: '25.00',
    day_change_pct: '0.0050',
    share: '0.125',
  },
  {
    id: id('hold', 4),
    account_id: ACCOUNT.brokerage,
    security_id: SECURITY.placement,
    symbol: 'XMPL',
    name: 'Example Private Placement',
    shares: '1',
    price: '5000.00',
    currency: 'USD',
    market_value: '5000.00',
    is_unquoted: true,
    cost_basis: '5000.00',
    total_gain: '0.00',
    total_gain_pct: '0',
    is_cost_basis_complete: true,
    day_change: null,
    day_change_pct: null,
    share: '0.125',
  },
]

const PORTFOLIO = {
  items: HOLDINGS,
  totals: {
    market_value: '40000.00',
    cost_basis: '30500.00',
    total_gain: '4500.00',
    day_change: null,
    day_change_pct: null,
    is_cost_basis_incomplete: true,
    is_day_change_incomplete: true,
    account_balance_not_held: '0.00',
    total_value: '40000.00',
  },
  allocation: HOLDINGS.map((holding) => ({
    security_id: holding.security_id,
    symbol: holding.symbol,
    share: holding.share,
    value: holding.market_value,
  })),
  allocation_by_class: [
    { key: 'us_stock', label: 'US stocks', share: '0.5', value: '20000.00' },
    { key: 'bond', label: 'Bonds', share: '0.25', value: '10000.00' },
    { key: 'intl_stock', label: 'International stocks', share: '0.125', value: '5000.00' },
    { key: 'other', label: 'Other', share: '0.125', value: '5000.00' },
  ],
  allocation_by_account: [{ key: ACCOUNT.brokerage, label: 'Example Brokerage', share: '1', value: '40000.00' }],
}

const NEWS = {
  items: [
    {
      id: id('news', 1),
      title: 'Sample Total Market Fund closes the quarter at a round number',
      publisher: 'Example Wire',
      url: 'https://example.com/news/1',
      thumbnail_url: null,
      symbols: ['SMPL'],
      published_at: '2026-06-15T13:00:00Z',
    },
    {
      id: id('news', 2),
      title:
        'Fake Bond Index Fund holders see an invented yield move that runs long enough to wrap onto a second line',
      publisher: 'Demo Markets Daily',
      url: 'https://example.com/news/2',
      thumbnail_url: null,
      symbols: ['FAKE', 'SMPL'],
      published_at: '2026-06-14T18:00:00Z',
    },
  ],
  available: true,
  unavailable: '',
  symbols: ['SMPL', 'FAKE', 'DEMO'],
}

const MONTH_ENDS = ['2026-01-31', '2026-02-28', '2026-03-31', '2026-04-30', '2026-05-31', '2026-06-15']
const VALUES = ['35000.00', '36000.00', '35500.00', '37000.00', '38500.00', '40000.00']
const RETURNS = ['0', '0.0200', '0.0100', '0.0400', '0.0600', '0.0800']

function performance(query: URLSearchParams) {
  return {
    window: { from: query.get('from') ?? '2026-01-01', to: query.get('to') ?? '2026-06-15', date_field: 'effective' },
    granularity: 'month',
    account_ids: query.getAll('account_id').length > 0 ? query.getAll('account_id') : [ACCOUNT.brokerage],
    points: MONTH_ENDS.map((on, index) => ({ on, value: VALUES[index], return_pct: RETURNS[index] })),
    twr: '0.08',
    irr: '0.075',
    twr_pct: '0.08',
    irr_pct: '0.075',
    start_value: '35000.00',
    end_value: '40000.00',
    net_flows: '2000.00',
  }
}

function activityRow(n: number, on: string, payee: string, amount: string, kind: string, pending = false) {
  return {
    transaction_id: id('invtx', n),
    account_id: ACCOUNT.brokerage,
    on,
    payee,
    statement_name: `${kind.toUpperCase()} ${payee.toUpperCase()}`,
    category_id: kind === 'dividend' || kind === 'interest' ? CATEGORY.interest : null,
    amount,
    kind,
    is_pending: pending,
  }
}

const ACTIVITY = {
  window: { from: '2026-01-01', to: '2026-06-15', date_field: 'effective' },
  account_ids: [ACCOUNT.brokerage],
  items: [
    activityRow(1, '2026-06-12', 'Sample Total Market Fund', '-1000.00', 'buy', true),
    activityRow(2, '2026-06-01', 'Fake Bond Index Fund', '40.00', 'dividend'),
    activityRow(3, '2026-06-01', 'Fake Bond Index Fund', '-40.00', 'reinvestment'),
    activityRow(4, '2026-05-15', 'Example Brokerage', '1000.00', 'contribution'),
    activityRow(5, '2026-05-01', 'Demo International Developed Markets Stock Index Fund Admiral Shares', '500.00', 'sell'),
    activityRow(6, '2026-04-30', 'Example Brokerage', '2.00', 'interest'),
    activityRow(7, '2026-03-31', 'Example Brokerage', '-10.00', 'fee'),
  ],
  summary: [
    { kind: 'buy', count: 1, total: '-1000.00' },
    { kind: 'sell', count: 1, total: '500.00' },
    { kind: 'dividend', count: 1, total: '40.00' },
    { kind: 'reinvestment', count: 1, total: '-40.00' },
    { kind: 'interest', count: 1, total: '2.00' },
    { kind: 'fee', count: 1, total: '-10.00' },
    { kind: 'contribution', count: 1, total: '1000.00' },
  ],
  income: '42.00',
  fees: '10.00',
}

function securityDetail(query: URLSearchParams) {
  return {
    security: SECURITIES[0],
    window: { from: query.get('from'), to: query.get('to'), date_field: 'effective' },
    positions: [HOLDINGS[0]],
    shares: '100',
    market_value: '20000.00',
    cost_basis: '15000.00',
    total_gain: '5000.00',
    total_gain_pct: '0.3333',
    day_change: '100.00',
    day_change_pct: '0.0050',
    is_cost_basis_incomplete: false,
    prices: MONTH_ENDS.map((on, index) => ({ on, close: String(180 + index * 4) })),
    price_change: '20',
    price_change_pct: '0.1111',
  }
}

/* ---- Reports ------------------------------------------------------------ */

function reportConfig(preset: string, mode: string, sign: string, columns = 'time') {
  return { preset, mode, rows: 'category', columns, time_grain: 'month', sign }
}

const SAVED_REPORTS = [
  {
    id: id('report', 1),
    name: 'Dining out this year',
    config: reportConfig('spending', 'transaction', 'expenses'),
    filter: { id: id('filter', 101), name: 'Dining out this year', scope: 'report', query_text: null, items: [] },
  },
  {
    id: id('report', 2),
    name: 'Everything the household spent on groceries, restaurants and takeout, month by month',
    config: reportConfig('spending_summary', 'summary', 'expenses'),
    filter: {
      id: id('filter', 102),
      name: 'Everything the household spent on groceries, restaurants and takeout, month by month',
      scope: 'report',
      query_text: null,
      items: [],
    },
  },
]

function reportRow(n: number, on: string, payee: string, account: string, category: string, amount: string) {
  return { transaction_id: id('rtx', n), split_id: null, on, payee, account_id: account, category_id: category, amount, notes: null }
}

const REPORT_GROUPS = [
  {
    key: CATEGORY.food,
    label: 'Food & Dining',
    depth: 0,
    total: '-600.00',
    count: 3,
    children: [
      {
        key: CATEGORY.groceries,
        label: 'Groceries',
        depth: 1,
        total: '-400.00',
        count: 2,
        children: [],
        transactions: [
          reportRow(1, '2026-06-10', 'Sample Grocery', ACCOUNT.card, CATEGORY.groceries, '-250.00'),
          reportRow(2, '2026-06-08', 'Sample Grocery', ACCOUNT.checking, CATEGORY.groceries, '-150.00'),
        ],
      },
      {
        key: CATEGORY.dining,
        label: 'Restaurants',
        depth: 1,
        total: '-200.00',
        count: 1,
        children: [],
        transactions: [reportRow(3, '2026-06-05', 'Example Bistro and Late-Night Noodle Counter', ACCOUNT.card, CATEGORY.dining, '-200.00')],
      },
    ],
    transactions: [],
  },
  {
    key: CATEGORY.home,
    label: 'Home',
    depth: 0,
    total: '-1600.00',
    count: 2,
    children: [
      {
        key: CATEGORY.rent,
        label: 'Mortgage & Rent',
        depth: 1,
        total: '-1500.00',
        count: 1,
        children: [],
        transactions: [reportRow(4, '2026-06-01', 'Example Mortgage Servicer', ACCOUNT.checking, CATEGORY.rent, '-1500.00')],
      },
      {
        key: CATEGORY.utilities,
        label: 'Utilities',
        depth: 1,
        total: '-100.00',
        count: 1,
        children: [],
        transactions: [reportRow(5, '2026-06-03', 'City Power & Light', ACCOUNT.checking, CATEGORY.utilities, '-100.00')],
      },
    ],
    transactions: [],
  },
]

const REPORT_MONTHS = [
  { key: '2026-04', label: 'Apr 2026' },
  { key: '2026-05', label: 'May 2026' },
  { key: '2026-06', label: 'Jun 2026' },
]

const REPORT_SUMMARY = {
  row_dimension: 'category',
  column_dimension: 'time',
  columns: REPORT_MONTHS,
  rows: [
    { key: CATEGORY.paycheck, label: 'Paycheck', section: 'Income', cells: ['4000.00', '4000.00', '4000.00'], total: '12000.00' },
    { key: CATEGORY.groceries, label: 'Groceries', section: 'Expenses', cells: ['-400.00', '-400.00', '-400.00'], total: '-1200.00' },
    { key: CATEGORY.dining, label: 'Restaurants', section: 'Expenses', cells: ['-200.00', '-200.00', '-200.00'], total: '-600.00' },
    { key: CATEGORY.rent, label: 'Mortgage & Rent', section: 'Expenses', cells: ['-1500.00', '-1500.00', '-1500.00'], total: '-4500.00' },
  ],
  sections: [
    { key: 'income', label: 'Income', cells: ['4000.00', '4000.00', '4000.00'], total: '12000.00' },
    { key: 'expenses', label: 'Expenses', cells: ['-2100.00', '-2100.00', '-2100.00'], total: '-6300.00' },
  ],
  column_totals: ['1900.00', '1900.00', '1900.00'],
  total: '5700.00',
}

function reportRun(query: URLSearchParams) {
  const mode = query.get('mode') ?? 'transaction'
  return {
    window: { from: query.get('from'), to: query.get('to'), date_field: 'effective' },
    config: {
      preset: 'custom',
      mode,
      rows: query.get('rows') ?? 'category',
      columns: query.get('columns') ?? 'time',
      time_grain: query.get('time_grain') ?? 'month',
      sign: query.get('sign') ?? 'both',
    },
    filter_id: query.get('filter_id'),
    totals: { income: '12000.00', expenses: '-6300.00', net: '5700.00', savings_rate: '0.475', count: 12 },
    transaction: mode === 'transaction' ? { groups: REPORT_GROUPS, total: '-2200.00', count: 5 } : null,
    summary: mode === 'summary' ? REPORT_SUMMARY : null,
  }
}

/** The Spending report on June 15: a month, quarter or year to date against the one before. */
function spendingReport(query: URLSearchParams) {
  const grain = query.get('grain') ?? 'month'
  const window = (key: string, from: string, through: string, end: string, partial = false) => ({
    key,
    from,
    through,
    end,
    partial,
  })
  const periods =
    grain === 'year'
      ? [2022, 2023, 2024, 2025, 2026].map((year) =>
          window(String(year), `${year}-01-01`, year === 2026 ? '2026-06-15' : `${year}-12-31`, `${year}-12-31`, year === 2026),
        )
      : grain === 'quarter'
        ? [
            window('2025-Q2', '2025-04-01', '2025-06-30', '2025-06-30'),
            window('2025-Q3', '2025-07-01', '2025-09-30', '2025-09-30'),
            window('2025-Q4', '2025-10-01', '2025-12-31', '2025-12-31'),
            window('2026-Q1', '2026-01-01', '2026-03-31', '2026-03-31'),
            window('2026-Q2', '2026-04-01', '2026-06-15', '2026-06-30', true),
          ]
        : Array.from({ length: 12 }, (_, index) => {
            const month = new Date(2025, 6 + index, 1)
            const key = `${month.getFullYear()}-${String(month.getMonth() + 1).padStart(2, '0')}`
            const last = new Date(month.getFullYear(), month.getMonth() + 1, 0).getDate()
            const end = `${key}-${last}`
            return index === 11 ? window(key, `${key}-01`, '2026-06-15', end, true) : window(key, `${key}-01`, end, end)
          })
  // February is a net credit: a refund outweighs the month's spending.
  const creditKey = '2026-02'
  const asked = query.get('period')
  const at = Math.max(
    0,
    periods.findIndex((one) => asked !== null && one.from <= asked && asked <= one.end),
  )
  const selectedAt = asked === null ? periods.length - 1 : at
  const selected = periods[selectedAt]!
  const prior = periods[Math.max(0, selectedAt - 1)]!
  const credit = selected.key === creditKey
  const difference = (amount: string, pct: string | null, state: string) => ({ amount, pct, state })
  const rows = [
    { key: CATEGORY.home, label: 'Home', amount: '-1800.00', comparison: '-1750.00', difference: difference('50.00', '2.86', 'change'), share: '0.6' },
    { key: CATEGORY.food, label: 'Food & Dining', amount: '-600.00', comparison: '-900.00', difference: difference('-300.00', '-33.33', 'change'), share: '0.2' },
    {
      key: CATEGORY.entertainment,
      label: 'Entertainment, concerts, the cinema and everything else on a weekend',
      amount: '-450.00',
      comparison: '0.00',
      difference: difference('450.00', null, 'new_spend'),
      share: '0.15',
    },
    { key: CATEGORY.auto, label: 'Auto & Transport', amount: '-150.00', comparison: '-150.00', difference: difference('0.00', '0', 'change'), share: '0.05' },
    { key: CATEGORY.shopping, label: 'Shopping', amount: '0.00', comparison: '-200.00', difference: difference('-200.00', '-100', 'no_spend'), share: null },
    { key: 'uncategorized', label: 'Uncategorized', amount: '120.00', comparison: '0.00', difference: difference('-120.00', null, 'new_spend'), share: null },
    ...(credit
      ? [{ key: CATEGORY.income, label: 'Tax refund', amount: '3500.00', comparison: '0.00', difference: difference('-3500.00', null, 'new_spend'), share: null }]
      : []),
  ]
  return {
    grain,
    today: '2026-06-15',
    period: selected,
    window: { from: selected.from, to: selected.through, date_field: 'effective' },
    periods: periods.map((one, index) => ({
      ...one,
      income: index % 3 === 0 ? '4000.00' : '3000.00',
      spent: one.key === creditKey ? '620.00' : one.partial ? '-2880.00' : `-${2000 + index * 150}.00`,
      remaining:
        one.key === creditKey
          ? '3620.00'
          : one.partial
            ? '-2880.00'
            : `${(index % 3 === 0 ? 2000 : 1000) - index * 150}.00`,
    })),
    compare: query.get('compare') ?? 'prior',
    compare_options:
      grain === 'year'
        ? ['prior', 'average_3', 'none']
        : grain === 'quarter'
          ? ['same_last_year', 'prior', 'ytd_average', 'average_2', 'average_4', 'none']
          : ['same_last_year', 'prior', 'ytd_average', 'average_3', 'average_6', 'average_12', 'none'],
    comparison:
      query.get('compare') === 'none'
        ? null
        : {
            compare: query.get('compare') ?? 'prior',
            periods: [{ ...prior, through: prior.from.slice(0, 8) + '15', partial: true }],
            average: false,
            spent: '-3000.00',
            difference: difference('-120.00', '-4', 'change'),
          },
    summary: credit
      ? {
          income: '3000.00',
          spent: '620.00',
          remaining: '3620.00',
          savings_rate: '1.2067',
          spending_rate: '-0.2067',
          rating: 'great',
          projection: null,
        }
      : {
          income: '0.00',
          spent: '-2880.00',
          remaining: '-2880.00',
          savings_rate: null,
          spending_rate: null,
          rating: 'good',
          // The paycheck lands on the 30th: 6,000.00 in and 1,500.00 of bills
          // still to come leave 1,620.00 of 6,000.00, 27%.
          projection: {
            end: selected.end,
            expected_income: '6000.00',
            expected_spent: '-1500.00',
            count: 3,
            income: '6000.00',
            spent: '-4380.00',
            remaining: '1620.00',
            savings_rate: '0.27',
            spending_rate: '0.73',
          },
        },
    group_by: query.get('group_by') ?? 'category',
    rows,
    uncategorized_count: 3,
    table: {
      periods,
      prior: { ...prior, through: prior.from.slice(0, 8) + '15', partial: true },
      rows: rows.map((row) => ({
        key: row.key,
        label: row.label,
        cells: periods.map((_, index) => (index === periods.length - 1 ? row.amount : row.comparison)),
        total: row.amount,
        difference: row.difference,
      })),
    },
    flow: {
      income: [
        { key: CATEGORY.paycheck, label: 'Paycheck', amount: '2500.00', share: '0.8333' },
        { key: CATEGORY.interest, label: 'Interest', amount: '500.00', share: '0.1667' },
      ],
      credits: [{ key: 'uncategorized', label: 'Uncategorized', amount: '120.00', share: '0.04' }],
      spending: rows
        .filter((row) => row.amount.startsWith('-'))
        .map((row) => ({ key: row.key, label: row.label, amount: row.amount.slice(1), share: row.share })),
      income_total: '3000.00',
      spent: '2880.00',
      spent_share: '0.96',
    },
  }
}

const MONTHLY_SUMMARY = {
  month: '2026-06',
  prior_month: '2026-05',
  income: '4000.00',
  expenses: '-2100.00',
  net: '1900.00',
  income_change_pct: '0',
  expenses_change_pct: '0.05',
  net_change_pct: '-0.05',
  bills: '-1600.00',
  discretionary: '-500.00',
  top_categories: [
    { key: CATEGORY.groceries, label: 'Groceries', total: '-400.00', count: 4, change_pct: '0.10' },
    { key: CATEGORY.dining, label: 'Restaurants', total: '-200.00', count: 3, change_pct: null },
  ],
  top_payees: [
    { key: 'sample-grocery', label: 'Sample Grocery', total: '-400.00', count: 4, change_pct: '0.10' },
    { key: 'example-bistro', label: 'Example Bistro and Late-Night Noodle Counter', total: '-200.00', count: 3, change_pct: null },
  ],
}

const SAVINGS_REPORT = {
  granularity: 'month',
  points: [
    { on: '2026-04-30', balance: '11000.00' },
    { on: '2026-05-31', balance: '11500.00' },
    { on: '2026-06-15', balance: '12000.00' },
  ],
  end: '12000.00',
  change: '1000.00',
  change_pct: '0.0909',
  months: ['2026-04', '2026-05', '2026-06'],
  accounts: [{ account_id: ACCOUNT.savings, name: 'Rainy Day Savings', cells: ['11000.00', '11500.00', '12000.00'] }],
  totals: ['11000.00', '11500.00', '12000.00'],
}

/* ---- Assistant ---------------------------------------------------------- */

const CONVERSATION = {
  long: id('conv', 1),
  short: id('conv', 2),
}

function conversation(conversationId: string, title: string, updated: string) {
  return {
    id: conversationId,
    title,
    created_at: updated,
    updated_at: updated,
    messages: [
      {
        id: id('msg', 1),
        role: 'user',
        content: 'How much did we spend on groceries last month?',
        tool_name: '',
        tool_arguments: null,
        created_at: updated,
      },
      {
        id: id('msg', 2),
        role: 'assistant',
        content: 'You spent $400.00 on groceries in May 2026, across 4 transactions.',
        tool_name: '',
        tool_arguments: null,
        created_at: updated,
      },
    ],
    actions: [],
    mail: null,
  }
}

const CONVERSATIONS = [
  conversation(
    CONVERSATION.long,
    'Why did the grocery and restaurant spending go up so much between April and June compared with last year',
    '2026-06-15T14:00:00Z',
  ),
  conversation(CONVERSATION.short, 'Budget check', '2026-06-10T14:00:00Z'),
]

const CONTEXT = {
  transaction: true,
  similar_transactions: 5,
  categories: true,
  rules: true,
  accounts: false,
  corrections: true,
  guidance: true,
  cash_flow_history: false,
}

const AUTOMATIONS = [
  {
    id: id('auto', 1),
    name: 'Categorize new transactions',
    description: 'Suggests a category for each new transaction.',
    is_enabled: true,
    trigger: 'transaction_arrived',
    trigger_config: { skip_transfers: true },
    filter_id: null,
    filter: null,
    prompt: 'Pick the best category for this transaction.',
    context: CONTEXT,
    mode: 'propose',
    tools: ['update_transaction'],
    model: '',
    max_tool_rounds: 4,
    confidence_threshold: 0.8,
    template_key: '',
    created_by: USER_ID,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-06-01T00:00:00Z',
    runs: 12,
    last_run_at: '2026-06-15T06:00:00Z',
    last_status: 'succeeded',
    pending_actions: 1,
  },
]

export const INVESTING = {
  'GET /holdings': PORTFOLIO,
  'GET /securities': SECURITIES,
  'GET /securities/:id': securityDetail,
  'GET /news': NEWS,
  'GET /performance': performance,
  'GET /investment-activity': ACTIVITY,

  'GET /reports': SAVED_REPORTS,
  'GET /reports/presets': [],
  'GET /reports/run': reportRun,
  'GET /reports/monthly-summary': MONTHLY_SUMMARY,
  'GET /reports/savings': SAVINGS_REPORT,
  'GET /reports/spending': spendingReport,

  'GET /assistant/conversations': CONVERSATIONS,
  'GET /assistant/conversations/:id': CONVERSATIONS[0],
  'GET /assistant-automations': AUTOMATIONS,
  'GET /assistant-automations/templates': [],
  'GET /assistant-automations/runs': [],
}
