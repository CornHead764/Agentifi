/**
 * Bills and income, cash flow, refunds, goals, the retirement planner,
 * watchlists, rules and guidance. Invented: round figures and names that
 * belong to nobody.
 */

import { ACCOUNT, CATEGORY, TAG, id } from './core'

type Kind = 'income' | 'bill' | 'subscription' | 'transfer' | 'credit_card_payment' | 'refund'

const MONTHLY = { frequency: 'MONTHLY', interval: 1, by_day: [], by_month: [] }

interface SeriesSeed {
  n: number
  kind: Kind
  description: string
  displayName?: string
  amount: string
  dueOn: string
  account?: string
  category: string | null
  recurrence?: object
  perYear?: number
  tags?: string[]
}

function series(seed: SeriesSeed) {
  const perYear = seed.perYear ?? 12
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
    recurrence: seed.recurrence ?? {
      alias: 'EVERY_MONTH',
      ...MONTHLY,
      by_month_day: [Number(seed.dueOn.slice(8))],
    },
    start_on: '2025-01-01',
    end_on: null,
    next_due_on: seed.dueOn,
    due_on: seed.dueOn,
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

const PAYCHECK = series({
  n: 1,
  kind: 'income',
  description: 'SAMPLE EMPLOYER PAYROLL',
  displayName: 'Sample Employer Payroll',
  amount: '2500.00',
  dueOn: '2026-06-15',
  category: CATEGORY.paycheck,
  recurrence: { alias: 'TWICE_A_MONTH', ...MONTHLY, by_month_day: [1, 15] },
  perYear: 24,
})
const RENT = series({
  n: 2,
  kind: 'bill',
  description: 'EXAMPLE PROPERTY MGMT',
  displayName: 'Example Property Management',
  amount: '-1500.00',
  dueOn: '2026-07-01',
  category: CATEGORY.rent,
})
const POWER = series({
  n: 3,
  kind: 'bill',
  description: 'CITY POWER & LIGHT',
  displayName: 'City Power & Light',
  amount: '-100.00',
  dueOn: '2026-06-18',
  category: CATEGORY.utilities,
})
const STREAMING = series({
  n: 4,
  kind: 'subscription',
  description: 'SAMPLE STREAMING SVC',
  displayName: 'Sample Streaming Service',
  amount: '-15.00',
  dueOn: '2026-06-20',
  account: ACCOUNT.card,
  category: CATEGORY.streaming,
})
const INSURANCE = series({
  n: 5,
  kind: 'bill',
  description: 'EXAMPLE MUTUAL AUTO INS',
  displayName: 'Example Mutual Auto and Home Insurance Premium With a Long Name',
  amount: '-600.00',
  dueOn: '2026-06-10',
  category: CATEGORY.auto,
  recurrence: { alias: 'EVERY_QUARTER', frequency: 'MONTHLY', interval: 3, by_month_day: [10], by_day: [], by_month: [] },
  perYear: 4,
  tags: [TAG.work],
})
const CARD_PAYMENT = series({
  n: 6,
  kind: 'credit_card_payment',
  description: 'Sample Rewards Card',
  amount: '-400.00',
  dueOn: '2026-06-25',
  category: CATEGORY.cardPayment,
})
const SAVINGS = series({
  n: 7,
  kind: 'transfer',
  description: 'Transfer to Rainy Day Savings',
  amount: '-200.00',
  dueOn: '2026-06-16',
  category: CATEGORY.transfer,
})
const REFUND = series({
  n: 8,
  kind: 'refund',
  description: 'EXAMPLE OUTFITTERS RETURN',
  displayName: 'Example Outfitters',
  amount: '60.00',
  dueOn: '2026-06-22',
  account: ACCOUNT.card,
  category: CATEGORY.shopping,
  recurrence: { alias: 'ONE_TIME', frequency: '', interval: 0, by_month_day: [], by_day: [], by_month: [] },
  perYear: 1,
})

const SERIES = [PAYCHECK, RENT, POWER, STREAMING, INSURANCE, CARD_PAYMENT, SAVINGS, REFUND]

type Status = 'upcoming' | 'past_due' | 'paid' | 'received' | 'skipped'

function occurrence(one: ReturnType<typeof series>, dueOn: string, status: Status, extra: object = {}) {
  return {
    series_id: one.id,
    account_id: one.account_id,
    category_id: one.category_id,
    kind: one.kind,
    label: one.label,
    due_on: dueOn,
    amount: one.amount,
    status,
    pays_on: dueOn,
    bill: null,
    bill_link: null,
    transaction_id: status === 'paid' || status === 'received' ? id('txn', Number(dueOn.slice(8))) : null,
    ...extra,
  }
}

const OCCURRENCES = [
  occurrence(PAYCHECK, '2026-06-01', 'received'),
  occurrence(INSURANCE, '2026-06-10', 'past_due'),
  // A pay-manually reminder: a provider's newest unpaid statement, with no
  // series and no account.
  {
    series_id: null,
    account_id: null,
    category_id: null,
    kind: 'bill',
    label: 'Example Health',
    due_on: '2026-06-14',
    amount: '-250.00',
    status: 'past_due',
    pays_on: null,
    bill: {
      id: id('bill', 2),
      amount_due: '250.00',
      due_on: '2026-06-14',
      status: 'open',
      source: 'provider',
      fetched_at: '2026-06-02T06:00:00Z',
      document_id: id('doc', 9),
    },
    bill_link: {
      connection_id: id('biller', 2),
      biller: 'mychart',
      connection_label: 'Example Health',
      subaccount_label: 'Guarantor account ending 0005 · Example Health Physicians Group',
      health: 'ok',
      autopay: false,
    },
    transaction_id: null,
  },
  occurrence(PAYCHECK, '2026-06-15', 'upcoming'),
  occurrence(SAVINGS, '2026-06-16', 'upcoming'),
  occurrence(POWER, '2026-06-18', 'upcoming', {
    amount: '-112.50',
    bill: {
      id: id('bill', 1),
      amount_due: '112.50',
      due_on: '2026-06-18',
      status: 'open',
      source: 'provider',
      fetched_at: '2026-06-14T06:00:00Z',
      document_id: null,
    },
    bill_link: {
      connection_id: id('biller', 1),
      biller: 'sample-power',
      connection_label: 'City Power & Light',
      subaccount_label: 'Service account ending 0000',
      health: 'ok',
      autopay: true,
    },
  }),
  occurrence(STREAMING, '2026-06-20', 'upcoming'),
  occurrence(REFUND, '2026-06-22', 'upcoming'),
  occurrence(CARD_PAYMENT, '2026-06-25', 'upcoming'),
  occurrence(RENT, '2026-07-01', 'upcoming'),
  occurrence(PAYCHECK, '2026-07-01', 'upcoming'),
  occurrence(POWER, '2026-07-18', 'upcoming'),
]

function occurrenceList(query: URLSearchParams) {
  return {
    window: { from: query.get('from'), to: query.get('to'), date_field: 'effective' },
    items: OCCURRENCES,
    summary: { income: '7560.00', expenses: '-3040.00', net: '4520.00', count: OCCURRENCES.length, past_due: 1 },
  }
}

function days(from: string, count: number): string[] {
  const start = Date.parse(`${from}T00:00:00Z`)
  return Array.from({ length: count }, (_, n) => new Date(start + n * 86_400_000).toISOString().slice(0, 10))
}

const CASH_FLOW_DAYS = days('2026-06-15', 31)

function line(accountId: string, name: string, start: number, dips: Record<string, number>) {
  let balance = start
  const points = CASH_FLOW_DAYS.map((on) => {
    balance += dips[on] ?? 0
    return { on, balance: balance.toFixed(2) }
  })
  const lowest = points.reduce((low, point) => (Number(point.balance) < Number(low.balance) ? point : low))
  return {
    account_id: accountId,
    name,
    starting_balance: start.toFixed(2),
    points,
    lowest,
    first_below: points.find((point) => Number(point.balance) < 500) ?? null,
  }
}

const CASH_FLOW_LINES = [
  line(ACCOUNT.checking, 'Everyday Checking', 5000, {
    '2026-06-15': 2500,
    '2026-06-16': -200,
    '2026-06-18': -112.5,
    '2026-06-25': -400,
    '2026-07-01': 1000,
    '2026-07-02': -6000,
    '2026-07-15': 2500,
  }),
  line(ACCOUNT.savings, 'Rainy Day Savings', 12000, { '2026-06-16': 200 }),
]

function watchlist(n: number, name: string, emoji: string | null, target: string | null, spent: number) {
  const limit = target === null ? null : Number(target)
  return {
    id: id('watch', n),
    name,
    emoji,
    filter_id: id('filter', 20 + n),
    period: 'month',
    this_month_spent: spent.toFixed(2),
    month_projection: (spent * 2).toFixed(2),
    year_to_date: (spent * 6).toFixed(2),
    monthly_trend: ['2026-01', '2026-02', '2026-03', '2026-04', '2026-05', '2026-06'].map((month, index) => ({
      month,
      spent: (index === 5 ? spent : spent * 2 - index * 10).toFixed(2),
      is_partial: index === 5,
    })),
    target_amount: target,
    left_to_target: limit === null ? null : (limit - spent).toFixed(2),
    pct_of_target: limit === null ? null : ((spent / limit) * 100).toFixed(2),
    is_over_target: limit !== null && spent > limit,
    is_projected_over_target: limit !== null && spent * 2 > limit,
    as_of: '2026-06-15',
  }
}

const WATCHLIST_DINING = watchlist(1, 'Eating out', '🍕', '400.00', 250)
const WATCHLIST_STREAMING = watchlist(2, 'Streaming and other monthly entertainment subscriptions', null, null, 45)

interface ItemSeed {
  field: string
  operator: string
  position?: number
  value_texts?: string[]
  text?: string
  amount_min?: string
  amount_max?: string
}

function item(n: number, seed: ItemSeed) {
  return {
    id: id('item', n),
    field: seed.field,
    operator: seed.operator,
    group_index: 0,
    position: seed.position ?? 0,
    negated: false,
    value_ids: [],
    value_texts: seed.value_texts ?? [],
    text: seed.text ?? null,
    amount_min: seed.amount_min ?? null,
    amount_max: seed.amount_max ?? null,
    date_from: null,
    date_to: null,
    date_preset: null,
    state: null,
  }
}

function ruleFilter(n: number, scope: string, items: ReturnType<typeof item>[]) {
  return { id: id('filter', n), name: null, scope, query_text: null, items }
}

function actions(set: object) {
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

function retirement() {
  const years = []
  let balance = 150000
  for (let year = 2026; year <= 2066; year += 1) {
    const age = year - 1986
    const working = age < 65
    const contributed = working ? 12000 : 0
    const drawn = working ? 0 : 60000
    const growth = Math.round(balance * 0.05)
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
      current_age: 40,
      retirement_age: 65,
      current_balance: '150000.00',
      is_balance_from_accounts: false,
      monthly_contribution: '1000.00',
      annual_return: '0.05',
      annual_inflation: '0.025',
      withdrawal_rate: '0.04',
      target_annual_income: '60000.00',
      life_expectancy: 80,
      annual_living_expenses: '60000.00',
      annual_retirement_income: '20000.00',
      pre_retirement_tax_rate: '0.2',
      post_retirement_tax_rate: '0.15',
      return_spread: '0.02',
      advanced: null,
    },
    years,
    years_to_retirement: 25,
    balance_at_retirement: atRetirement.balance,
    balance_at_retirement_in_todays_dollars: atRetirement.balance_in_todays_dollars,
    total_contributed: '300000.00',
    total_growth: '500000.00',
    annual_income: '40000.00',
    annual_income_in_todays_dollars: '21500.00',
    meets_target: false,
    shortfall: '20000.00',
    runs_out_at_age: null,
  }
}

function goalSpending(n: number, date: string, payee: string, amount: string) {
  return {
    transaction_id: id('txn', n),
    kind: 'spending',
    date,
    account_id: ACCOUNT.card,
    account_name: 'Sample Rewards Card',
    payee,
    amount,
    saved: '0.00',
    spent: amount.slice(1),
  }
}

export const PLANNING = {
  'GET /occurrences': occurrenceList,
  'GET /cash-flow': (query: URLSearchParams) => ({
    window: { from: query.get('from'), to: query.get('to'), date_field: 'effective' },
    threshold: query.get('threshold') ?? '500.00',
    accounts: CASH_FLOW_LINES,
    combined: CASH_FLOW_DAYS.map((on, n) => ({
      on,
      balance: CASH_FLOW_LINES.reduce((sum, one) => sum + Number(one.points[n].balance), 0).toFixed(2),
    })),
    occurrences: OCCURRENCES.filter((one) => one.due_on >= '2026-06-15'),
  }),
  'GET /cash-flow-forecast': {
    available: false,
    unavailable: 'No forecast has been written for these accounts yet.',
  },

  'GET /series': SERIES,
  'GET /series/suggested': (query: URLSearchParams) =>
    query.get('dismissed') === 'true'
      ? []
      : [
          {
            signature: 'sample-gym',
            account_id: ACCOUNT.card,
            category_id: CATEGORY.entertainment,
            kind: 'subscription',
            description: 'EXAMPLE FITNESS CLUB',
            display_name: 'Example Fitness Club',
            label: 'Example Fitness Club',
            amount: '-40.00',
            currency: 'USD',
            recurrence: { alias: 'EVERY_MONTH', ...MONTHLY, by_month_day: [5] },
            start_on: '2026-07-05',
            occurrences: 6,
            first_seen: '2026-01-05',
            last_seen: '2026-06-05',
            confidence: 0.9,
            match_criteria: 'auto',
            match_amount_min: null,
            match_amount_max: null,
            transaction_ids: [id('txn', 101), id('txn', 102)],
          },
          {
            signature: 'sample-water',
            account_id: ACCOUNT.checking,
            category_id: CATEGORY.utilities,
            kind: 'bill',
            description: 'SAMPLE WATER DISTRICT',
            display_name: 'Sample Water District',
            label: 'Sample Water District',
            amount: '-50.00',
            currency: 'USD',
            recurrence: { alias: 'EVERY_X_DAYS', frequency: 'DAILY', interval: 60, by_month_day: [], by_day: [], by_month: [] },
            start_on: '2026-07-20',
            occurrences: 3,
            first_seen: '2026-01-20',
            last_seen: '2026-05-20',
            confidence: 0.6,
            match_criteria: 'range',
            match_amount_min: '-60.00',
            match_amount_max: '-40.00',
            transaction_ids: [id('txn', 103)],
          },
        ],
  'GET /series/refunds': {
    expected: [{ series: REFUND, expected_on: '2026-06-22', settled_on: null, transaction_id: null }],
    completed: [],
  },

  'GET /goals': [
    {
      id: id('goal', 1),
      name: 'Summer vacation',
      emoji: '🏖️',
      account_id: ACCOUNT.savings,
      account_name: 'Rainy Day Savings',
      funding_account_ids: [ACCOUNT.checking],
      funding: [{ account_id: ACCOUNT.checking, account_name: 'Everyday Checking', saved: '1000.00' }],
      target_amount: '2000.00',
      target_on: '2026-08-01',
      completed_on: null,
      closed_on: null,
      stage: 'spending',
      is_taken_from_plan: true,
      saved_so_far: '800.00',
      withdrawn: '1200.00',
      spent_on_goal: '200.00',
      spending_by_category: [
        { category_id: CATEGORY.dining, category_name: 'Restaurants', spent: '200.00', transaction_count: 2 },
      ],
      unassigned_withdrawn: '1000.00',
      funded: '2000.00',
      contributed_this_month: '200.00',
      left_to_save: '1200.00',
      monthly_needed: null,
      months_to_target: 2,
      target_has_passed: false,
      pct_complete: '40.00',
      pct_funded: '100.00',
      is_complete: false,
      is_funded: true,
      txn_ids: [id('txn', 201), id('txn', 202), id('txn', 203), id('txn', 204)],
      withdrawal_txn_ids: [id('txn', 204)],
      spending_txn_ids: [id('txn', 202), id('txn', 203)],
      contributions: [
        goalSpending(202, '2026-06-06', 'Sample Pizza Place', '-120.00'),
        goalSpending(203, '2026-06-12', 'Example Cafe', '-80.00'),
        {
          transaction_id: id('txn', 204),
          kind: 'withdrawal',
          date: '2026-06-04',
          account_id: ACCOUNT.savings,
          account_name: 'Rainy Day Savings',
          payee: 'Transfer to Everyday Checking',
          amount: '-1200.00',
          saved: '-1200.00',
          spent: '0.00',
        },
        {
          transaction_id: id('txn', 201),
          kind: 'contribution',
          date: '2026-06-01',
          account_id: ACCOUNT.checking,
          account_name: 'Everyday Checking',
          payee: 'Transfer to Rainy Day Savings',
          amount: '2000.00',
          saved: '2000.00',
          spent: '0.00',
        },
      ],
    },
    {
      id: id('goal', 2),
      name: 'Emergency fund for six months of household expenses',
      emoji: null,
      account_id: ACCOUNT.savings,
      account_name: 'Rainy Day Savings',
      funding_account_ids: [],
      funding: [],
      target_amount: '20000.00',
      target_on: null,
      completed_on: null,
      closed_on: null,
      stage: 'saving',
      is_taken_from_plan: false,
      saved_so_far: '5000.00',
      withdrawn: '0.00',
      spent_on_goal: '0.00',
      spending_by_category: [],
      unassigned_withdrawn: '0.00',
      funded: '5000.00',
      contributed_this_month: '0.00',
      left_to_save: '15000.00',
      monthly_needed: null,
      months_to_target: null,
      target_has_passed: false,
      pct_complete: '25.00',
      pct_funded: '25.00',
      is_complete: false,
      is_funded: false,
      txn_ids: [],
      withdrawal_txn_ids: [],
      spending_txn_ids: [],
      contributions: [],
    },
    {
      id: id('goal', 3),
      name: 'New laptop',
      emoji: '💻',
      account_id: ACCOUNT.checking,
      account_name: 'Everyday Checking',
      funding_account_ids: [],
      funding: [],
      target_amount: '1000.00',
      target_on: '2026-05-01',
      completed_on: '2026-04-20',
      closed_on: null,
      stage: 'funded',
      is_taken_from_plan: false,
      saved_so_far: '1000.00',
      withdrawn: '0.00',
      spent_on_goal: '0.00',
      spending_by_category: [],
      unassigned_withdrawn: '0.00',
      funded: '1000.00',
      contributed_this_month: '0.00',
      left_to_save: '0.00',
      monthly_needed: null,
      months_to_target: 0,
      target_has_passed: true,
      pct_complete: '100.00',
      pct_funded: '100.00',
      is_complete: true,
      is_funded: true,
      txn_ids: [],
      withdrawal_txn_ids: [],
      spending_txn_ids: [],
      contributions: [],
    },
    {
      id: id('goal', 4),
      name: 'Garden shed',
      emoji: '🪴',
      account_id: ACCOUNT.savings,
      account_name: 'Rainy Day Savings',
      funding_account_ids: [],
      funding: [],
      target_amount: '600.00',
      target_on: null,
      completed_on: null,
      closed_on: '2026-05-15',
      stage: 'closed',
      is_taken_from_plan: true,
      saved_so_far: '50.00',
      withdrawn: '550.00',
      spent_on_goal: '540.00',
      spending_by_category: [],
      unassigned_withdrawn: '10.00',
      funded: '600.00',
      contributed_this_month: '0.00',
      left_to_save: '550.00',
      monthly_needed: null,
      months_to_target: null,
      target_has_passed: false,
      pct_complete: '8.33',
      pct_funded: '100.00',
      is_complete: false,
      is_funded: true,
      txn_ids: [],
      withdrawal_txn_ids: [],
      spending_txn_ids: [],
      contributions: [],
    },
  ],

  'GET /goals/:id/suggestions': (query: URLSearchParams) => ({
    kind: query.get('kind') ?? 'spending',
    from: '2026-05-18',
    to: '2026-09-30',
    truncated: false,
    rows: [
      {
        transaction_id: id('txn', 211),
        date: '2026-06-03',
        account_id: ACCOUNT.card,
        account_name: 'Sample Rewards Card',
        payee: 'Example Beach Rentals and Watersports Equipment',
        amount: '-340.00',
        category_name: 'Restaurants',
        matches_category: true,
        matches_payee: false,
        days_from_withdrawal: 2,
      },
      {
        transaction_id: id('txn', 212),
        date: '2026-06-20',
        account_id: ACCOUNT.checking,
        account_name: 'Everyday Checking',
        payee: 'Sample Ferry',
        amount: '-85.00',
        category_name: '',
        matches_category: false,
        matches_payee: false,
        days_from_withdrawal: null,
      },
    ],
  }),

  'GET /planning/retirement': retirement(),

  'GET /watchlists': [WATCHLIST_DINING, WATCHLIST_STREAMING],
  'GET /watchlists/:id': (query: URLSearchParams) => ({
    ...WATCHLIST_DINING,
    month: query.get('month') ?? '2026-06',
    spent: '250.00',
    by_category: [{ key: CATEGORY.dining, label: 'Restaurants', spent: '250.00', share: '1' }],
    by_payee: [
      { key: 'Sample Pizza Place', label: 'Sample Pizza Place', spent: '150.00', share: '0.6' },
      { key: 'Example Cafe', label: 'Example Cafe', spent: '100.00', share: '0.4' },
    ],
    by_tag: [],
  }),

  'GET /rules': [
    {
      id: id('rule', 1),
      name: 'Streaming charges',
      filter_id: id('filter', 1),
      filter: ruleFilter(1, 'rule', [
        item(1, { field: 'statement_name', operator: 'contains', text: 'SAMPLE STREAMING' }),
      ]),
      owns_filter: true,
      priority: 1,
      is_active: true,
      actions: actions({ set_payee: 'Sample Streaming Service', set_category_id: CATEGORY.streaming }),
    },
    {
      id: id('rule', 2),
      name: 'Business lunches at the cafe near the office, tagged and excluded from the spending plan',
      filter_id: id('filter', 2),
      filter: ruleFilter(2, 'rule', [
        item(2, { field: 'payee', operator: 'in', value_texts: ['Example Cafe'] }),
        item(3, { field: 'amount', operator: 'between', amount_min: '-50.00', amount_max: '-10.00', position: 1 }),
      ]),
      owns_filter: true,
      priority: 2,
      is_active: true,
      actions: actions({
        set_category_id: CATEGORY.dining,
        add_tag_ids: [TAG.work],
        set_excluded_from_spending_plan: true,
      }),
    },
    {
      id: id('rule', 3),
      name: 'Fuel',
      filter_id: id('filter', 3),
      filter: ruleFilter(3, 'rule', [item(4, { field: 'text', operator: 'contains', text: 'SAMPLE FUEL' })]),
      owns_filter: true,
      priority: 3,
      is_active: false,
      actions: actions({ set_category_id: CATEGORY.fuel }),
    },
  ],
  'GET /rules/:id/preview': {
    rule_id: id('rule', 1),
    matched: 1,
    changed: 1,
    unchanged: 0,
    truncated: false,
    since: null,
    changes: [
      {
        transaction_id: id('txn', 301),
        date: '2026-06-01',
        account_name: 'Sample Rewards Card',
        statement_name: 'SAMPLE STREAMING SVC',
        payee: 'SAMPLE STREAMING SVC',
        amount: '-15.00',
        actions: actions({ set_payee: 'Sample Streaming Service', set_category_id: CATEGORY.streaming }),
      },
    ],
  },

  'GET /guidance': [
    {
      id: id('guidance', 1),
      name: 'Warehouse club trips',
      instruction:
        'Split warehouse club receipts between Groceries and Shopping by what the order lists; a trip under $50.00 is all Groceries.',
      filter_id: id('filter', 11),
      filter: ruleFilter(11, 'guidance', [
        item(11, { field: 'payee', operator: 'in', value_texts: ['Sample Warehouse Club'] }),
      ]),
      owns_filter: true,
      applies_to: 'Transactions whose payee is Sample Warehouse Club.',
      is_active: true,
      position: 1,
    },
    {
      id: id('guidance', 2),
      name: 'Transfers',
      instruction: 'Never categorize a transfer between my own accounts as income.',
      filter_id: id('filter', 12),
      filter: ruleFilter(12, 'guidance', []),
      owns_filter: true,
      applies_to: 'Every transaction.',
      is_active: false,
      position: 2,
    },
  ],
}
