/**
 * Investments and Reports for the Sample household. Invented: tickers that
 * trade nowhere, prices that never were, and headlines at example.com.
 */

import { ACCOUNT, CATEGORY, accountName, categoryName, id, parentOf } from './core'
import { MONTHS, TODAY_ISO, TRANSACTIONS, isSpending, money, sum, type Transaction } from './ledger'

/* ---- Holdings ------------------------------------------------------------ */

interface SecuritySeed {
  n: number
  symbol: string
  name: string
  price: number
  prior: number
}

function security(seed: SecuritySeed) {
  return {
    id: id('sec', seed.n),
    symbol: seed.symbol,
    name: seed.name,
    kind: 'fund',
    exchange: 'NASDAQ',
    currency: 'USD',
    last_price: money(seed.price),
    prior_close: money(seed.prior),
    last_price_at: '2026-06-15T15:00:00Z',
  }
}

const SECURITIES = [
  security({ n: 1, symbol: 'SMPL', name: 'Sample Total Market Index Fund', price: 210, prior: 208.6 }),
  security({ n: 2, symbol: 'DEMO', name: 'Demo International Index Fund', price: 92, prior: 92.4 }),
  security({ n: 3, symbol: 'FAKE', name: 'Fake Bond Index Fund', price: 50, prior: 49.95 }),
  security({ n: 4, symbol: 'XMPL', name: 'Example Target Date 2050 Fund', price: 50, prior: 49.7 }),
]

const MARKET_VALUE = 174500

function holding(n: number, account: string, sec: (typeof SECURITIES)[number], shares: number, basis: number) {
  const price = Number(sec.last_price)
  const value = shares * price
  const dayChange = shares * (price - Number(sec.prior_close))
  return {
    id: id('hold', n),
    account_id: account,
    security_id: sec.id,
    symbol: sec.symbol,
    name: sec.name,
    shares: String(shares),
    price: sec.last_price,
    currency: 'USD',
    market_value: money(value),
    is_unquoted: false,
    cost_basis: money(basis),
    total_gain: money(value - basis),
    total_gain_pct: ((value - basis) / basis).toFixed(4),
    is_cost_basis_complete: true,
    day_change: money(dayChange),
    day_change_pct: ((dayChange / (value - dayChange)) * 100).toFixed(2),
    share: (value / MARKET_VALUE).toFixed(4),
  }
}

const HOLDINGS = [
  holding(1, ACCOUNT.brokerage, SECURITIES[0], 120, 19800),
  holding(2, ACCOUNT.brokerage, SECURITIES[1], 150, 12600),
  holding(3, ACCOUNT.brokerage, SECURITIES[2], 180, 9300),
  holding(4, ACCOUNT.retirement, SECURITIES[3], 2530, 98000),
]

const CASH_IN_BROKERAGE = 48200 - 25200 - 13800 - 9000

function totals() {
  const value = HOLDINGS.reduce((total, one) => total + Number(one.market_value), 0)
  const basis = HOLDINGS.reduce((total, one) => total + Number(one.cost_basis), 0)
  const day = HOLDINGS.reduce((total, one) => total + Number(one.day_change), 0)
  return {
    market_value: money(value),
    cost_basis: money(basis),
    total_gain: money(value - basis),
    day_change: money(day),
    day_change_pct: ((day / (value - day)) * 100).toFixed(2),
    is_cost_basis_incomplete: false,
    is_day_change_incomplete: false,
    account_balance_not_held: money(CASH_IN_BROKERAGE),
    total_value: money(value + CASH_IN_BROKERAGE),
  }
}

const PORTFOLIO = {
  items: HOLDINGS,
  totals: totals(),
  allocation: HOLDINGS.map((one) => ({ security_id: one.security_id, symbol: one.symbol, share: one.share, value: one.market_value })),
  allocation_by_class: [
    { key: 'us_stock', label: 'US stocks', share: '0.6100', value: '106445.00' },
    { key: 'intl_stock', label: 'International stocks', share: '0.2150', value: '37517.50' },
    { key: 'bond', label: 'Bonds', share: '0.1750', value: '30537.50' },
  ],
  allocation_by_account: [
    { key: ACCOUNT.retirement, label: 'Retirement 401(k)', share: '0.7249', value: '126500.00' },
    { key: ACCOUNT.brokerage, label: 'Example Brokerage', share: '0.2751', value: '48000.00' },
  ],
}

const NEWS = {
  items: [
    {
      id: id('news', 1),
      title: 'Index funds close the quarter near the top of their range',
      publisher: 'Example Wire',
      url: 'https://example.com/news/1',
      thumbnail_url: null,
      symbols: ['SMPL'],
      published_at: '2026-06-15T13:00:00Z',
    },
    {
      id: id('news', 2),
      title: 'Bond funds steady as rates hold for a third month',
      publisher: 'Demo Markets Daily',
      url: 'https://example.com/news/2',
      thumbnail_url: null,
      symbols: ['FAKE'],
      published_at: '2026-06-14T18:00:00Z',
    },
    {
      id: id('news', 3),
      title: 'Target-date funds rebalance toward bonds ahead of the summer',
      publisher: 'Sample Finance Review',
      url: 'https://example.com/news/3',
      thumbnail_url: null,
      symbols: ['XMPL'],
      published_at: '2026-06-13T15:00:00Z',
    },
  ],
  available: true,
  unavailable: '',
  symbols: ['SMPL', 'DEMO', 'FAKE', 'XMPL'],
}

const MONTH_ENDS = ['2025-12-31', '2026-01-31', '2026-02-28', '2026-03-31', '2026-04-30', '2026-05-31', '2026-06-15']
const VALUES = [160400, 163100, 161800, 166200, 169900, 172300, 174700]

/** One account's share of the portfolio and how much more or less it moved than the whole. */
const PERFORMANCE_SHAPE: Record<string, { share: number; swing: number }> = {
  [ACCOUNT.brokerage]: { share: 48200 / 174700, swing: 1.45 },
  [ACCOUNT.retirement]: { share: 126500 / 174700, swing: 0.83 },
}

function performance(query: URLSearchParams) {
  const asked = query.getAll('account_id')
  const shape = asked.length === 1 ? (PERFORMANCE_SHAPE[asked[0]] ?? { share: 1, swing: 1 }) : { share: 1, swing: 1 }
  const start = VALUES[0]
  const returns = VALUES.map((value, index) => ((value - start - index * 1200) / start) * 100 * shape.swing)
  const last = returns[returns.length - 1]
  return {
    window: { from: query.get('from') ?? '2026-01-01', to: query.get('to') ?? TODAY_ISO, date_field: 'effective' },
    granularity: 'month',
    account_ids: asked.length > 0 ? asked : [ACCOUNT.brokerage, ACCOUNT.retirement],
    points: MONTH_ENDS.map((on, index) => ({ on, value: money(VALUES[index] * shape.share), return_pct: returns[index].toFixed(2) })),
    twr: (last * 0.01).toFixed(4),
    irr: (last * 0.0097).toFixed(4),
    twr_pct: last.toFixed(2),
    irr_pct: (last * 0.97).toFixed(2),
    start_value: money(start * shape.share),
    end_value: money(VALUES[VALUES.length - 1] * shape.share),
    net_flows: money(7200 * shape.share),
  }
}

function activityRow(n: number, on: string, account: string, payee: string, amount: string, kind: string) {
  return {
    transaction_id: id('invtx', n),
    account_id: account,
    on,
    payee,
    statement_name: `${kind.toUpperCase()} ${payee.toUpperCase()}`,
    category_id: kind === 'dividend' || kind === 'interest' ? CATEGORY.dividends : null,
    amount,
    kind,
    is_pending: false,
  }
}

const ACTIVITY_ITEMS = [
  activityRow(1, '2026-06-15', ACCOUNT.retirement, 'Example Target Date 2050 Fund', '-600.00', 'buy'),
  activityRow(2, '2026-06-15', ACCOUNT.retirement, 'Payroll contribution', '600.00', 'contribution'),
  activityRow(3, '2026-06-01', ACCOUNT.brokerage, 'Fake Bond Index Fund', '31.50', 'dividend'),
  activityRow(4, '2026-06-01', ACCOUNT.brokerage, 'Fake Bond Index Fund', '-31.50', 'reinvestment'),
  activityRow(5, '2026-06-01', ACCOUNT.retirement, 'Example Target Date 2050 Fund', '-600.00', 'buy'),
  activityRow(6, '2026-06-01', ACCOUNT.retirement, 'Payroll contribution', '600.00', 'contribution'),
  activityRow(7, '2026-04-10', ACCOUNT.brokerage, 'Sample Total Market Index Fund', '-1000.00', 'buy'),
  activityRow(8, '2026-03-31', ACCOUNT.brokerage, 'Sample Total Market Index Fund', '96.00', 'dividend'),
]

const ACTIVITY = {
  window: { from: '2026-01-01', to: TODAY_ISO, date_field: 'effective' },
  account_ids: [ACCOUNT.brokerage, ACCOUNT.retirement],
  items: ACTIVITY_ITEMS,
  summary: [
    { kind: 'buy', count: 3, total: '-2200.00' },
    { kind: 'dividend', count: 2, total: '127.50' },
    { kind: 'reinvestment', count: 1, total: '-31.50' },
    { kind: 'contribution', count: 2, total: '1200.00' },
  ],
  income: '127.50',
  fees: '0.00',
}

function securityDetail(_query: URLSearchParams, _body: unknown, path: string) {
  const sec = SECURITIES.find((one) => one.id === path.split('/')[2]) ?? SECURITIES[0]
  const position = HOLDINGS.find((one) => one.security_id === sec.id) ?? HOLDINGS[0]
  const price = Number(sec.last_price)
  return {
    security: sec,
    window: { from: '2026-01-01', to: TODAY_ISO, date_field: 'effective' },
    positions: [position],
    shares: position.shares,
    market_value: position.market_value,
    cost_basis: position.cost_basis,
    total_gain: position.total_gain,
    total_gain_pct: position.total_gain_pct,
    day_change: position.day_change,
    day_change_pct: position.day_change_pct,
    is_cost_basis_incomplete: false,
    prices: MONTH_ENDS.map((on, index) => ({ on, close: money(price * (0.92 + index * 0.0135)) })),
    price_change: money(price * 0.08),
    price_change_pct: '0.0870',
  }
}

/* ---- Reports ------------------------------------------------------------- */

function spendIn(rows: Transaction[], from: string, to: string): Transaction[] {
  return rows.filter((txn) => txn.date >= from && txn.date <= to)
}

/** Spending by top-level category, positive, with split parts filed by their own category. */
function byTopCategory(rows: Transaction[]): Map<string, number> {
  const totals = new Map<string, number>()
  for (const txn of rows) {
    if (!isSpending(txn)) continue
    const parts = txn.splits.length > 0 ? txn.splits.map((split) => ({ category: split.category_id, amount: split.amount })) : [{ category: txn.category_id, amount: txn.amount }]
    for (const part of parts) {
      const top = parentOf(part.category) ?? part.category ?? 'uncategorized'
      totals.set(top, (totals.get(top) ?? 0) - Number(part.amount))
    }
  }
  return totals
}

function incomeIn(rows: Transaction[]): number {
  return rows
    .filter((txn) => Number(txn.amount) > 0 && !txn.transfer_pair_id && txn.category_id !== CATEGORY.transfer && txn.category_id !== CATEGORY.cardPayment)
    .reduce((total, txn) => total + Number(txn.amount), 0)
}

function spentIn(rows: Transaction[]): number {
  return [...byTopCategory(rows).values()].reduce((total, one) => total + one, 0)
}

function lastDay(month: string): string {
  const [year, number] = month.split('-').map(Number)
  return `${month}-${String(new Date(year, number, 0).getDate()).padStart(2, '0')}`
}

/** Months before the history starts carry plausible totals rather than rows. */
const EARLIER = [
  { key: '2025-07', income: 6500, spent: 4610 },
  { key: '2025-08', income: 6500, spent: 4980 },
  { key: '2025-09', income: 6500, spent: 4420 },
  { key: '2025-10', income: 9750, spent: 4730 },
  { key: '2025-11', income: 6500, spent: 5120 },
  { key: '2025-12', income: 6500, spent: 5890 },
]

function spendingReport(query: URLSearchParams) {
  const grain = query.get('grain') ?? 'month'
  const months = [...EARLIER.map((one) => one.key), ...MONTHS]
  const periods = months.map((key) => {
    const partial = key === '2026-06'
    return { key, from: `${key}-01`, through: partial ? TODAY_ISO : lastDay(key), end: lastDay(key), partial }
  })
  const asked = query.get('period')
  const found = periods.findIndex((one) => asked !== null && one.from <= asked && asked <= one.end)
  const selectedAt = found >= 0 ? found : periods.length - 1
  const selected = periods[selectedAt]
  const prior = periods[Math.max(0, selectedAt - 1)]
  const priorThrough = selected.partial ? `${prior.key}-15` : prior.through

  const current = byTopCategory(spendIn(TRANSACTIONS, selected.from, selected.through))
  const before = byTopCategory(spendIn(TRANSACTIONS, prior.from, priorThrough))
  const spentNow = [...current.values()].reduce((total, one) => total + one, 0)
  const keys = [...new Set([...current.keys(), ...before.keys()])].sort((a, b) => (current.get(b) ?? 0) - (current.get(a) ?? 0))
  const difference = (amount: number, pct: number | null, stateName: string) => ({ amount: money(amount), pct: pct === null ? null : pct.toFixed(2), state: stateName })
  const rows = keys.map((key) => {
    const now = current.get(key) ?? 0
    const then = before.get(key) ?? 0
    const delta = now - then
    return {
      key,
      label: key === 'uncategorized' ? 'Uncategorized' : categoryName(key),
      amount: money(-now),
      comparison: money(-then),
      difference: difference(delta, then === 0 ? null : (delta / then) * 100, now === 0 ? 'no_spend' : then === 0 ? 'new_spend' : 'change'),
      share: spentNow === 0 || now === 0 ? null : (now / spentNow).toFixed(4),
    }
  })

  const totalsFor = (period: (typeof periods)[number]) => {
    const earlier = EARLIER.find((one) => one.key === period.key)
    if (earlier) return { income: earlier.income, spent: earlier.spent }
    const window = spendIn(TRANSACTIONS, period.from, period.through)
    return { income: incomeIn(window), spent: spentIn(window) }
  }
  const selectedTotals = totalsFor(selected)
  const priorWindow = spendIn(TRANSACTIONS, prior.from, priorThrough)
  const priorSpent = spentIn(priorWindow)
  const expectedBills = 118 + 15 + 55 + 70 + 85
  const projectedSpent = selectedTotals.spent + expectedBills + (selectedTotals.spent - 2235) * 0.5

  return {
    grain,
    today: TODAY_ISO,
    period: selected,
    window: { from: selected.from, to: selected.through, date_field: 'effective' },
    periods: periods.map((one) => {
      const total = totalsFor(one)
      return { ...one, income: money(total.income), spent: money(-total.spent), remaining: money(total.income - total.spent) }
    }),
    compare: query.get('compare') ?? 'prior',
    compare_options: ['same_last_year', 'prior', 'ytd_average', 'average_3', 'average_6', 'average_12', 'none'],
    comparison:
      query.get('compare') === 'none'
        ? null
        : {
            compare: query.get('compare') ?? 'prior',
            periods: [{ ...prior, through: priorThrough, partial: selected.partial }],
            average: false,
            spent: money(-priorSpent),
            difference: difference(spentNow - priorSpent, priorSpent === 0 ? null : ((spentNow - priorSpent) / priorSpent) * 100, 'change'),
          },
    summary: {
      income: money(selectedTotals.income),
      spent: money(-selectedTotals.spent),
      remaining: money(selectedTotals.income - selectedTotals.spent),
      savings_rate: ((selectedTotals.income - selectedTotals.spent) / selectedTotals.income).toFixed(4),
      spending_rate: (selectedTotals.spent / selectedTotals.income).toFixed(4),
      rating: 'good',
      projection: selected.partial
        ? {
            end: selected.end,
            expected_income: '0.00',
            expected_spent: money(-expectedBills),
            count: 5,
            income: money(selectedTotals.income),
            spent: money(-projectedSpent),
            remaining: money(selectedTotals.income - projectedSpent),
            savings_rate: ((selectedTotals.income - projectedSpent) / selectedTotals.income).toFixed(4),
            spending_rate: (projectedSpent / selectedTotals.income).toFixed(4),
          }
        : null,
    },
    group_by: query.get('group_by') ?? 'category',
    rows,
    uncategorized_count: 5,
    table: {
      periods,
      prior: { ...prior, through: priorThrough, partial: selected.partial },
      rows: rows.map((row) => ({
        key: row.key,
        label: row.label,
        cells: periods.map((one) => {
          if (EARLIER.some((early) => early.key === one.key)) return money(-(Number(row.share ?? 0.05) * (EARLIER.find((early) => early.key === one.key)?.spent ?? 0)))
          return money(-(byTopCategory(spendIn(TRANSACTIONS, one.from, one.through)).get(row.key) ?? 0))
        }),
        total: row.amount,
        difference: row.difference,
      })),
    },
    flow: {
      income: [{ key: CATEGORY.paycheck, label: 'Paycheck', amount: money(selectedTotals.income), share: '1' }],
      credits: [],
      spending: rows
        .filter((row) => Number(row.amount) < 0)
        .map((row) => ({ key: row.key, label: row.label, amount: row.amount.slice(1), share: row.share })),
      income_total: money(selectedTotals.income),
      spent: money(selectedTotals.spent),
      spent_share: (selectedTotals.spent / selectedTotals.income).toFixed(4),
    },
  }
}

function reportRow(txn: Transaction) {
  return { transaction_id: txn.id, split_id: null, on: txn.date, payee: txn.payee, account_id: txn.account_id, category_id: txn.category_id, amount: txn.amount, notes: null }
}

function reportGroups(from: string, to: string) {
  const rows = spendIn(TRANSACTIONS, from, to).filter((txn) => isSpending(txn) && txn.category_id !== null)
  const tops = new Map<string, Transaction[]>()
  for (const txn of rows) {
    const top = parentOf(txn.category_id) ?? txn.category_id ?? 'uncategorized'
    tops.set(top, [...(tops.get(top) ?? []), txn])
  }
  return [...tops.entries()]
    .map(([top, members]) => {
      const leaves = new Map<string, Transaction[]>()
      for (const txn of members) leaves.set(txn.category_id ?? top, [...(leaves.get(txn.category_id ?? top) ?? []), txn])
      return {
        key: top,
        label: categoryName(top),
        depth: 0,
        total: sum(members.map((txn) => txn.amount)),
        count: members.length,
        children: [...leaves.entries()].map(([leaf, leafRows]) => ({
          key: leaf,
          label: categoryName(leaf),
          depth: 1,
          total: sum(leafRows.map((txn) => txn.amount)),
          count: leafRows.length,
          children: [],
          transactions: leafRows.map(reportRow),
        })),
        transactions: [],
      }
    })
    .sort((a, b) => Number(a.total) - Number(b.total))
}

function monthsBetween(from: string, to: string): string[] {
  const months: string[] = []
  let [year, month] = from.slice(0, 7).split('-').map(Number)
  const last = to.slice(0, 7)
  for (;;) {
    const key = `${year}-${String(month).padStart(2, '0')}`
    if (key > last) return months
    months.push(key)
    month += 1
    if (month > 12) {
      month = 1
      year += 1
    }
  }
}

/** One month's spending by top-level category and its income, from the rows or, before them, from the earlier totals. */
function monthFigures(month: string): { spending: Map<string, number>; income: number } {
  const earlier = EARLIER.find((one) => one.key === month)
  if (!earlier) {
    const window = spendIn(TRANSACTIONS, `${month}-01`, lastDay(month))
    return { spending: byTopCategory(window), income: incomeIn(window) }
  }
  const pattern = byTopCategory(spendIn(TRANSACTIONS, '2026-01-01', '2026-01-31'))
  const patternTotal = [...pattern.values()].reduce((all, one) => all + one, 0)
  const spending = new Map([...pattern.entries()].map(([key, value]) => [key, Math.round((value / patternTotal) * earlier.spent * 100) / 100]))
  return { spending, income: earlier.income }
}

function reportSummary(query: URLSearchParams) {
  const sign = query.get('sign') ?? 'both'
  const keys = monthsBetween(query.get('from') ?? '2026-04-01', query.get('to') ?? TODAY_ISO).filter((key) => key >= EARLIER[0].key)
  const columns = keys.map((key) => ({ key, label: new Date(`${key}-15T12:00:00`).toLocaleString('en-US', { month: 'short', year: 'numeric' }) }))
  const figures = keys.map(monthFigures)
  const total = (cells: number[]) => money(cells.reduce((all, one) => all + one, 0))
  const categories = [...new Set(figures.flatMap((one) => [...one.spending.keys()]))]
  const expenseRows = categories.map((key) => {
    const cells = figures.map((one) => -(one.spending.get(key) ?? 0))
    return { key: key === 'uncategorized' ? '' : key, label: key === 'uncategorized' ? 'Uncategorized' : categoryName(key), section: 'Expenses', cells: cells.map(money), total: total(cells) }
  })
  const incomeCells = figures.map((one) => one.income)
  const incomeRows = [{ key: CATEGORY.paycheck, label: 'Paycheck', section: 'Income', cells: incomeCells.map(money), total: total(incomeCells) }]
  const spentCells = figures.map((one) => -[...one.spending.values()].reduce((all, value) => all + value, 0))
  const rows = sign === 'income' ? incomeRows : sign === 'expenses' ? expenseRows : [...incomeRows, ...expenseRows]
  const columnTotals = figures.map((_, index) => (sign === 'income' ? incomeCells[index] : sign === 'expenses' ? spentCells[index] : incomeCells[index] + spentCells[index]))
  return {
    row_dimension: 'category',
    column_dimension: 'time',
    columns,
    rows,
    sections: [
      ...(sign === 'expenses' ? [] : [{ key: 'income', label: 'Income', cells: incomeCells.map(money), total: total(incomeCells) }]),
      ...(sign === 'income' ? [] : [{ key: 'expenses', label: 'Expenses', cells: spentCells.map(money), total: total(spentCells) }]),
    ],
    column_totals: columnTotals.map(money),
    total: total(columnTotals),
  }
}

function reportRun(query: URLSearchParams) {
  const mode = query.get('mode') ?? 'transaction'
  const from = query.get('from') ?? '2026-06-01'
  const to = query.get('to') ?? TODAY_ISO
  const window = spendIn(TRANSACTIONS, from, to)
  const income = incomeIn(window)
  const spent = spentIn(window)
  const groups = reportGroups(from, to)
  return {
    window: { from, to, date_field: 'effective' },
    config: {
      preset: 'custom',
      mode,
      rows: query.get('rows') ?? 'category',
      columns: query.get('columns') ?? 'time',
      time_grain: query.get('time_grain') ?? 'month',
      sign: query.get('sign') ?? 'both',
    },
    filter_id: query.get('filter_id'),
    totals: { income: money(income), expenses: money(-spent), net: money(income - spent), savings_rate: income === 0 ? null : ((income - spent) / income).toFixed(4), count: window.length },
    transaction: mode === 'transaction' ? { groups, total: sum(groups.map((one) => one.total)), count: groups.reduce((all, one) => all + one.count, 0) } : null,
    summary: mode === 'summary' ? reportSummary(query) : null,
  }
}

const SAVED_REPORTS = [
  {
    id: id('report', 1),
    name: 'Dining out this year',
    config: { preset: 'spending', mode: 'transaction', rows: 'category', columns: 'time', time_grain: 'month', sign: 'expenses' },
    filter: { id: id('filter', 101), name: 'Dining out this year', scope: 'report', query_text: null, items: [] },
  },
  {
    id: id('report', 2),
    name: 'HSA spending for taxes',
    config: { preset: 'spending_summary', mode: 'summary', rows: 'category', columns: 'time', time_grain: 'month', sign: 'expenses' },
    filter: { id: id('filter', 102), name: 'HSA spending for taxes', scope: 'report', query_text: null, items: [] },
  },
]

function payeeIn(rows: Transaction[], payee: string) {
  const mine = rows.filter((txn) => txn.payee === payee)
  return { total: money(mine.reduce((total, txn) => total + Number(txn.amount), 0)), count: mine.length }
}

function monthlySummary() {
  const june = spendIn(TRANSACTIONS, '2026-06-01', TODAY_ISO)
  const may = spendIn(TRANSACTIONS, '2026-05-01', '2026-05-31')
  const income = incomeIn(june)
  const spent = spentIn(june)
  const tops = [...byTopCategory(june).entries()].sort((a, b) => b[1] - a[1]).slice(0, 4)
  const mayTops = byTopCategory(may)
  return {
    month: '2026-06',
    prior_month: '2026-05',
    income: money(income),
    expenses: money(-spent),
    net: money(income - spent),
    income_change_pct: '0',
    expenses_change_pct: ((spent - spentIn(may)) / spentIn(may)).toFixed(4),
    net_change_pct: null,
    bills: '-2629.00',
    discretionary: money(-(spent - 2629)),
    top_categories: tops.map(([key, value]) => ({
      key,
      label: categoryName(key),
      total: money(-value),
      count: june.filter((txn) => (parentOf(txn.category_id) ?? txn.category_id) === key).length,
      change_pct: mayTops.get(key) ? ((value - (mayTops.get(key) ?? 0)) / (mayTops.get(key) ?? 1)).toFixed(4) : null,
    })),
    top_payees: [
      { key: 'example-market', label: 'Example Market', ...payeeIn(june, 'Example Market'), change_pct: '-0.12' },
      { key: 'costco', label: 'Costco', ...payeeIn(june, 'Costco'), change_pct: '0.04' },
    ],
  }
}

const SAVINGS_REPORT = {
  granularity: 'month',
  points: MONTHS.map((month, index) => ({ on: month === '2026-06' ? TODAY_ISO : lastDay(month), balance: money(16300 + index * 440) })),
  end: '18500.00',
  change: '2200.00',
  change_pct: '0.1350',
  months: MONTHS,
  accounts: [{ account_id: ACCOUNT.savings, name: accountName(ACCOUNT.savings), cells: MONTHS.map((_, index) => money(16300 + index * 440)) }],
  totals: MONTHS.map((_, index) => money(16300 + index * 440)),
}

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
  'GET /reports/monthly-summary': monthlySummary(),
  'GET /reports/savings': SAVINGS_REPORT,
  'GET /reports/spending': spendingReport,
}
