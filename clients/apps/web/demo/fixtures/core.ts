/**
 * The demo household's accounts, categories and tags. Invented: Sam Sample
 * banks at Example Bank and Sample Credit Union, and every figure is made up.
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
export const CREDIT_UNION = id('inst', 2)
export const BROKER = id('inst', 3)
export const CONNECTION = id('conn', 1)

export const ACCOUNT = {
  checking: id('acct', 1),
  savings: id('acct', 2),
  card: id('acct', 3),
  cashback: id('acct', 4),
  hsa: id('acct', 5),
  brokerage: id('acct', 6),
  retirement: id('acct', 7),
  house: id('acct', 8),
  car: id('acct', 9),
  mortgage: id('acct', 10),
  autoLoan: id('acct', 11),
}

interface AccountSeed {
  id: string
  name: string
  kind: 'cash' | 'credit_card' | 'loan' | 'investment' | 'asset'
  type: string
  balance: string
  institution: string | null
  sort: number
  mask?: string
  creditLimit?: string
  statement?: { balance: string; minimum: string; due: string }
  rate?: string
  equity?: { value: string; owed: string; equity: string; ltv: string; loan: string }
  securedBy?: string
  flags?: { excluded_from_reports?: boolean; excluded_from_spending_plan?: boolean; requires_receipts?: boolean }
}

function account(seed: AccountSeed) {
  const connected = seed.institution !== null
  return {
    id: seed.id,
    name: seed.name,
    description: null,
    notes: null,
    kind: seed.kind,
    type: seed.type,
    currency: 'USD',
    institution_id: seed.institution,
    connection_id: connected ? CONNECTION : null,
    masked_number: connected ? (seed.mask ?? '0000') : null,
    logo_url: null,
    custom_logo_url: null,
    sort_order: seed.sort,
    provider_balance: connected ? seed.balance : null,
    provider_balance_at: connected ? '2026-06-15T06:00:00Z' : null,
    withheld_balance: null,
    withheld_balance_at: null,
    withheld_balance_reason: '',
    accept_zero_balance: false,
    opening_balance: '0.00',
    opening_balance_on: '2025-01-01',
    goal_balance: '0.00',
    pending_holds: '0.00',
    credit_limit: seed.creditLimit ?? null,
    statement_balance: seed.statement?.balance ?? null,
    minimum_due: seed.statement?.minimum ?? null,
    due_date: seed.statement?.due ?? null,
    statement_source: null,
    interest_rate: seed.rate ?? null,
    statement_close_day: seed.kind === 'credit_card' ? 1 : null,
    excluded_from_reports: false,
    excluded_from_spending_plan: false,
    excluded_from_account_bar: false,
    include_in_net_worth: true,
    exclude_bank_pending: false,
    requires_receipts: false,
    is_closed: false,
    closed_on: null,
    simplefin_account_id: connected ? `demo-${seed.sort}` : null,
    sync_floor_on: null,
    history_starts_on: null,
    hide_below_balance: null,
    default_register_tab: null,
    property_address: seed.type === 'real_estate' ? '100 Example Street, Springfield' : null,
    vehicle_vin: null,
    vehicle_mileage: seed.type === 'vehicle' ? 42000 : null,
    vehicle_mileage_as_of: seed.type === 'vehicle' ? '2026-06-01' : null,
    vehicle_miles_per_year: seed.type === 'vehicle' ? 12000 : null,
    valuation_source: null,
    valued_at: null,
    secured_by_account_id: seed.securedBy ?? null,
    balances: {
      balance: seed.balance,
      balance_with_pending: seed.balance,
      available_balance: seed.balance,
      credit_used_pct: seed.creditLimit
        ? (Math.abs(Number(seed.balance)) / Number(seed.creditLimit)).toFixed(4)
        : null,
    },
    equity: seed.equity
      ? {
          value: seed.equity.value,
          owed: seed.equity.owed,
          equity: seed.equity.equity,
          loan_to_value: seed.equity.ltv,
          loan_ids: [seed.equity.loan],
        }
      : null,
    history: { starts_on: '2025-01-01', automatic_starts_on: '2025-01-01' },
    hidden_small_balance: false,
    ...seed.flags,
  }
}

export const ACCOUNTS = [
  account({ id: ACCOUNT.checking, name: 'Everyday Checking', kind: 'cash', type: 'checking', balance: '6420.00', institution: INSTITUTION, sort: 1, mask: '1111' }),
  account({ id: ACCOUNT.savings, name: 'High-Yield Savings', kind: 'cash', type: 'savings', balance: '18500.00', institution: INSTITUTION, sort: 2, mask: '2222' }),
  account({ id: ACCOUNT.hsa, name: 'Health Savings (HSA)', kind: 'cash', type: 'hsa', balance: '4850.00', institution: CREDIT_UNION, sort: 3, mask: '3333', flags: { requires_receipts: true } }),
  account({ id: ACCOUNT.card, name: 'Sample Rewards Visa', kind: 'credit_card', type: 'credit_card', balance: '-1285.00', institution: INSTITUTION, sort: 4, mask: '4444', creditLimit: '12000.00', statement: { balance: '-1100.00', minimum: '35.00', due: '2026-06-25' } }),
  account({ id: ACCOUNT.cashback, name: 'Cash Back Card', kind: 'credit_card', type: 'credit_card', balance: '-340.00', institution: CREDIT_UNION, sort: 5, mask: '5555', creditLimit: '6000.00', statement: { balance: '-298.50', minimum: '25.00', due: '2026-06-28' } }),
  account({ id: ACCOUNT.brokerage, name: 'Example Brokerage', kind: 'investment', type: 'brokerage', balance: '48200.00', institution: BROKER, sort: 6, mask: '6666' }),
  account({ id: ACCOUNT.retirement, name: 'Retirement 401(k)', kind: 'investment', type: '401k', balance: '126500.00', institution: BROKER, sort: 7, mask: '7777' }),
  account({ id: ACCOUNT.house, name: 'Home', kind: 'asset', type: 'real_estate', balance: '420000.00', institution: null, sort: 8, equity: { value: '420000.00', owed: '268400.00', equity: '151600.00', ltv: '0.6390', loan: ACCOUNT.mortgage }, flags: { excluded_from_reports: true, excluded_from_spending_plan: true } }),
  account({ id: ACCOUNT.car, name: 'Family Car', kind: 'asset', type: 'vehicle', balance: '18000.00', institution: null, sort: 9, equity: { value: '18000.00', owed: '9200.00', equity: '8800.00', ltv: '0.5111', loan: ACCOUNT.autoLoan }, flags: { excluded_from_reports: true, excluded_from_spending_plan: true } }),
  account({ id: ACCOUNT.mortgage, name: 'Mortgage', kind: 'loan', type: 'mortgage', balance: '-268400.00', institution: CREDIT_UNION, sort: 10, mask: '1010', rate: '0.0425', securedBy: ACCOUNT.house }),
  account({ id: ACCOUNT.autoLoan, name: 'Auto Loan', kind: 'loan', type: 'auto_loan', balance: '-9200.00', institution: CREDIT_UNION, sort: 11, mask: '1111', rate: '0.0390', securedBy: ACCOUNT.car }),
]

export function accountName(accountId: string): string {
  return ACCOUNTS.find((one) => one.id === accountId)?.name ?? ''
}

export const CATEGORY = {
  income: id('cat', 1),
  paycheck: id('cat', 2),
  interest: id('cat', 3),
  dividends: id('cat', 4),
  home: id('cat', 10),
  mortgage: id('cat', 11),
  utilities: id('cat', 12),
  internet: id('cat', 13),
  homeImprovement: id('cat', 14),
  household: id('cat', 15),
  food: id('cat', 20),
  groceries: id('cat', 21),
  dining: id('cat', 22),
  coffee: id('cat', 23),
  auto: id('cat', 30),
  fuel: id('cat', 31),
  autoInsurance: id('cat', 32),
  carPayment: id('cat', 33),
  health: id('cat', 35),
  doctor: id('cat', 36),
  pharmacy: id('cat', 37),
  dental: id('cat', 38),
  gym: id('cat', 39),
  entertainment: id('cat', 40),
  streaming: id('cat', 41),
  hobbies: id('cat', 42),
  shopping: id('cat', 50),
  clothing: id('cat', 51),
  electronics: id('cat', 52),
  books: id('cat', 53),
  personal: id('cat', 55),
  kids: id('cat', 60),
  school: id('cat', 61),
  gifts: id('cat', 65),
  travel: id('cat', 70),
  phone: id('cat', 75),
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
  category(CATEGORY.dividends, 'Dividends', 'income', CATEGORY.income, 4),
  category(CATEGORY.home, 'Home', 'expense', null, 10),
  category(CATEGORY.mortgage, 'Mortgage', 'expense', CATEGORY.home, 11),
  category(CATEGORY.utilities, 'Utilities', 'expense', CATEGORY.home, 12),
  category(CATEGORY.internet, 'Internet', 'expense', CATEGORY.home, 13),
  category(CATEGORY.homeImprovement, 'Home Improvement', 'expense', CATEGORY.home, 14),
  category(CATEGORY.household, 'Household Supplies', 'expense', CATEGORY.home, 15),
  category(CATEGORY.food, 'Food & Dining', 'expense', null, 20),
  category(CATEGORY.groceries, 'Groceries', 'expense', CATEGORY.food, 21),
  category(CATEGORY.dining, 'Restaurants', 'expense', CATEGORY.food, 22),
  category(CATEGORY.coffee, 'Coffee Shops', 'expense', CATEGORY.food, 23),
  category(CATEGORY.auto, 'Auto & Transport', 'expense', null, 30),
  category(CATEGORY.fuel, 'Gas & Fuel', 'expense', CATEGORY.auto, 31),
  category(CATEGORY.autoInsurance, 'Auto Insurance', 'expense', CATEGORY.auto, 32),
  category(CATEGORY.carPayment, 'Car Payment', 'expense', CATEGORY.auto, 33),
  category(CATEGORY.health, 'Health & Fitness', 'expense', null, 35),
  category(CATEGORY.doctor, 'Doctor', 'expense', CATEGORY.health, 36),
  category(CATEGORY.pharmacy, 'Pharmacy', 'expense', CATEGORY.health, 37),
  category(CATEGORY.dental, 'Dentist', 'expense', CATEGORY.health, 38),
  category(CATEGORY.gym, 'Gym', 'expense', CATEGORY.health, 39),
  category(CATEGORY.entertainment, 'Entertainment', 'expense', null, 40),
  category(CATEGORY.streaming, 'Streaming Services', 'expense', CATEGORY.entertainment, 41),
  category(CATEGORY.hobbies, 'Hobbies', 'expense', CATEGORY.entertainment, 42),
  category(CATEGORY.shopping, 'Shopping', 'expense', null, 50),
  category(CATEGORY.clothing, 'Clothing', 'expense', CATEGORY.shopping, 51),
  category(CATEGORY.electronics, 'Electronics', 'expense', CATEGORY.shopping, 52),
  category(CATEGORY.books, 'Books', 'expense', CATEGORY.shopping, 53),
  category(CATEGORY.personal, 'Personal Care', 'expense', null, 55),
  category(CATEGORY.kids, 'Kids', 'expense', null, 60),
  category(CATEGORY.school, 'School Supplies', 'expense', CATEGORY.kids, 61),
  category(CATEGORY.gifts, 'Gifts & Donations', 'expense', null, 65),
  category(CATEGORY.travel, 'Travel', 'expense', null, 70),
  category(CATEGORY.phone, 'Mobile Phone', 'expense', null, 75),
  category(CATEGORY.transfer, 'Transfers', 'transfer', null, 90),
  category(CATEGORY.cardPayment, 'Credit Card Payment', 'transfer', CATEGORY.transfer, 91),
]

export function categoryName(categoryId: string | null): string {
  return CATEGORIES.find((one) => one.id === categoryId)?.name ?? 'Uncategorized'
}

export function parentOf(categoryId: string | null): string | null {
  return CATEGORIES.find((one) => one.id === categoryId)?.parent_id ?? null
}

export const TAG = { vacation: id('tag', 1), hsa: id('tag', 2), taxDeductible: id('tag', 3) }

export const TAGS = [
  { id: TAG.vacation, name: 'Summer trip', color: '#3b82f6' },
  { id: TAG.hsa, name: 'HSA eligible', color: '#22c55e' },
  { id: TAG.taxDeductible, name: 'Tax deductible', color: null },
]

export const CORE = {
  'GET /accounts': ACCOUNTS,
  'GET /categories': CATEGORIES,
  'GET /tags': TAGS,
}
