/**
 * The accounts, categories and tags every page reads. Invented: round figures
 * and names that belong to nobody.
 */

/** A stable UUID per fixture: `id('acct', 1)`. */
export function id(kind: string, n: number): string {
  const prefix = Array.from(kind)
    .map((char) => char.charCodeAt(0).toString(16))
    .join('')
    .padEnd(8, '0')
    .slice(0, 8)
  return `${prefix}-0000-4000-8000-${String(n).padStart(12, '0')}`
}

export const INSTITUTION = id('inst', 1)
export const CONNECTION = id('conn', 1)

export const ACCOUNT = {
  checking: id('acct', 1),
  savings: id('acct', 2),
  card: id('acct', 3),
  brokerage: id('acct', 4),
  house: id('acct', 5),
  mortgage: id('acct', 6),
}

interface AccountSeed {
  id: string
  name: string
  kind: 'cash' | 'credit_card' | 'loan' | 'investment' | 'asset'
  type: string
  balance: string
  connected: boolean
  sort: number
  creditLimit?: string
  /** Exclusions set on the account, so a flagged row sits among plain ones. */
  flags?: { excluded_from_reports?: boolean; excluded_from_spending_plan?: boolean; requires_receipts?: boolean }
}

function account(seed: AccountSeed) {
  return {
    id: seed.id,
    name: seed.name,
    description: null,
    notes: null,
    kind: seed.kind,
    type: seed.type,
    currency: 'USD',
    institution_id: seed.connected ? INSTITUTION : null,
    connection_id: seed.connected ? CONNECTION : null,
    masked_number: seed.connected ? '0000' : null,
    logo_url: null,
    custom_logo_url: null,
    sort_order: seed.sort,
    provider_balance: seed.connected ? seed.balance : null,
    provider_balance_at: seed.connected ? '2026-06-15T06:00:00Z' : null,
    withheld_balance: null,
    withheld_balance_at: null,
    withheld_balance_reason: '',
    accept_zero_balance: false,
    opening_balance: '0.00',
    opening_balance_on: '2025-01-01',
    goal_balance: '0.00',
    pending_holds: '0.00',
    credit_limit: seed.creditLimit ?? null,
    statement_balance: seed.kind === 'credit_card' ? '-400.00' : null,
    minimum_due: seed.kind === 'credit_card' ? '25.00' : null,
    due_date: seed.kind === 'credit_card' ? '2026-06-25' : null,
    statement_source: null,
    interest_rate: seed.kind === 'loan' ? '0.05' : null,
    statement_close_day: seed.kind === 'credit_card' ? 1 : null,
    excluded_from_reports: false,
    excluded_from_spending_plan: false,
    excluded_from_account_bar: false,
    include_in_net_worth: true,
    exclude_bank_pending: false,
    requires_receipts: false,
    is_closed: false,
    closed_on: null,
    simplefin_account_id: seed.connected ? `sample-${seed.sort}` : null,
    sync_floor_on: null,
    history_starts_on: null,
    hide_below_balance: null,
    default_register_tab: null,
    property_address: seed.kind === 'asset' ? 'Sample address' : null,
    vehicle_vin: null,
    vehicle_mileage: null,
    vehicle_mileage_as_of: null,
    vehicle_miles_per_year: null,
    valuation_source: null,
    valued_at: null,
    secured_by_account_id: seed.id === ACCOUNT.mortgage ? ACCOUNT.house : null,
    balances: {
      balance: seed.balance,
      balance_with_pending: seed.balance,
      available_balance: seed.balance,
      credit_used_pct: seed.creditLimit ? '0.08' : null,
    },
    equity:
      seed.id === ACCOUNT.house
        ? {
            value: '300000.00',
            owed: '200000.00',
            equity: '100000.00',
            loan_to_value: '0.6667',
            loan_ids: [ACCOUNT.mortgage],
          }
        : null,
    history: { starts_on: '2025-01-01', automatic_starts_on: '2025-01-01' },
    hidden_small_balance: false,
    ...seed.flags,
  }
}

export const ACCOUNTS = [
  account({ id: ACCOUNT.checking, name: 'Everyday Checking', kind: 'cash', type: 'checking', balance: '5000.00', connected: true, sort: 1 }),
  account({ id: ACCOUNT.savings, name: 'Rainy Day Savings', kind: 'cash', type: 'savings', balance: '12000.00', connected: true, sort: 2 }),
  account({ id: ACCOUNT.card, name: 'Sample Rewards Card', kind: 'credit_card', type: 'credit_card', balance: '-800.00', connected: true, sort: 3, creditLimit: '10000.00', flags: { requires_receipts: true } }),
  account({ id: ACCOUNT.brokerage, name: 'Example Brokerage', kind: 'investment', type: 'brokerage', balance: '40000.00', connected: true, sort: 4 }),
  account({ id: ACCOUNT.house, name: 'Home', kind: 'asset', type: 'real_estate', balance: '300000.00', connected: false, sort: 5, flags: { excluded_from_reports: true, excluded_from_spending_plan: true } }),
  account({ id: ACCOUNT.mortgage, name: 'Home Mortgage Thirty Year Fixed Rate Loan', kind: 'loan', type: 'mortgage', balance: '-200000.00', connected: true, sort: 6 }),
]

export const CATEGORY = {
  income: id('cat', 1),
  paycheck: id('cat', 2),
  interest: id('cat', 3),
  home: id('cat', 10),
  rent: id('cat', 11),
  utilities: id('cat', 12),
  food: id('cat', 20),
  groceries: id('cat', 21),
  dining: id('cat', 22),
  auto: id('cat', 30),
  fuel: id('cat', 31),
  entertainment: id('cat', 40),
  streaming: id('cat', 41),
  shopping: id('cat', 50),
  transfer: id('cat', 90),
  cardPayment: id('cat', 91),
}

function category(
  categoryId: string,
  name: string,
  kind: 'income' | 'expense' | 'transfer',
  parent: string | null,
  sort: number,
) {
  return {
    id: categoryId,
    parent_id: parent,
    name,
    kind,
    known_category_id: null,
    txf_id: null,
    txf_ids: [],
    is_user_assignable: true,
    is_editable: true,
    protected_reason: null,
    excluded_from_reports: kind === 'transfer',
    excluded_from_spending_plan: kind === 'transfer',
    excluded_from_category_list: false,
    sort_order: sort,
  }
}

export const CATEGORIES = [
  category(CATEGORY.income, 'Income', 'income', null, 1),
  category(CATEGORY.paycheck, 'Paycheck', 'income', CATEGORY.income, 2),
  category(CATEGORY.interest, 'Interest', 'income', CATEGORY.income, 3),
  category(CATEGORY.home, 'Home', 'expense', null, 10),
  category(CATEGORY.rent, 'Mortgage & Rent', 'expense', CATEGORY.home, 11),
  category(CATEGORY.utilities, 'Utilities', 'expense', CATEGORY.home, 12),
  category(CATEGORY.food, 'Food & Dining', 'expense', null, 20),
  category(CATEGORY.groceries, 'Groceries', 'expense', CATEGORY.food, 21),
  category(CATEGORY.dining, 'Restaurants', 'expense', CATEGORY.food, 22),
  category(CATEGORY.auto, 'Auto & Transport', 'expense', null, 30),
  category(CATEGORY.fuel, 'Gas & Fuel', 'expense', CATEGORY.auto, 31),
  category(CATEGORY.entertainment, 'Entertainment', 'expense', null, 40),
  category(CATEGORY.streaming, 'Streaming Services', 'expense', CATEGORY.entertainment, 41),
  category(CATEGORY.shopping, 'Shopping', 'expense', null, 50),
  category(CATEGORY.transfer, 'Transfers', 'transfer', null, 90),
  category(CATEGORY.cardPayment, 'Credit Card Payment', 'transfer', CATEGORY.transfer, 91),
]

export const TAG = { vacation: id('tag', 1), work: id('tag', 2) }

export const TAGS = [
  { id: TAG.vacation, name: 'Vacation', color: '#3b82f6' },
  { id: TAG.work, name: 'Work expense', color: null },
]

export const CORE = {
  'GET /accounts': ACCOUNTS,
  'GET /categories': CATEGORIES,
  'GET /tags': TAGS,
}
