/**
 * The register's wire types, with the server's field names unconverted: a
 * camelCase mapper is where `statement_name` would leak onto an update body.
 */

import type { Money } from '@/lib/money'

export type Uuid = string
/** `YYYY-MM-DD`. Never a `Date`: a register row has no time and no zone. */
export type IsoDate = string
export type TransactionSource =
  | 'sync'
  | 'manual'
  | 'file_import'
  | 'simplifi_import'
  | 'opening_balance'
  | 'balance_adjustment'

export type AccountKind = 'cash' | 'credit_card' | 'loan' | 'investment' | 'asset'
export type CategoryKind = 'income' | 'expense' | 'transfer'

export interface Split {
  id: Uuid
  position: number
  amount: Money
  category_id: Uuid | null
  memo: string | null
  tag_ids: Uuid[]
}

/**
 * Where a row on an account that requires receipts stands: none behind it, a
 * document behind it (attached, or a bill's or an order's), or settled by hand
 * as needing none.
 */
export type ReceiptStatus = 'missing' | 'on_file' | 'not_needed'

export interface Transaction {
  id: Uuid
  account_id: Uuid
  /** The date the register shows; reporting reads `effective_date`. */
  date: IsoDate
  /** When it hits cash flow. Null means "same as `date`". */
  effective_date: IsoDate | null

  amount: Money
  currency: string
  amount_primary: Money | null
  fx_rate_used: string | null

  /** The bank's wording. Read-only, at every layer including this one. */
  statement_name: string
  payee: string
  /** The provider's second line; read-only like `statement_name`. Empty when the feed sent none. */
  memo: string
  /** The purchase date where the feed sent one apart from posting; null otherwise. */
  transacted_on: IsoDate | null
  /** Whatever else the feed sent, untyped on purpose: its shape is the provider's. */
  provider_extra?: Record<string, unknown>
  notes: string | null
  check_number: string | null
  category_id: Uuid | null
  source: TransactionSource

  is_pending: boolean
  is_reviewed: boolean
  excluded_from_reports: boolean
  excluded_from_spending_plan: boolean
  is_bill: boolean
  is_subscription: boolean

  transfer_pair_id: Uuid | null
  /** On the income row a padding mail rule writes: the purchase it pads. */
  padded_txn_id: Uuid | null
  /** On a purchase paid by payroll deduction: the income row that pads it. */
  padding_txn_id: Uuid | null
  user_flag: string | null
  user_flag_note: string | null
  series_id: Uuid | null
  series_due_on: IsoDate | null
  balance: Money | null

  splits: Split[]
  /** Only on a filtered page's partly kept split row: the kept splits and their primary-currency sum. */
  matched_split_ids?: Uuid[] | null
  matched_amount?: Money | null
  tag_ids: Uuid[]
  attachment_count: number
  /** Null on a row its account does not hold to a receipt. */
  receipt_status: ReceiptStatus | null
  /** A person said the row needs no receipt. */
  receipt_not_needed: boolean
  /** An undecided assistant proposal for this row. */
  suggestion: Suggestion | null
  /** A category check queued or running now, per the server so a reopened register still shows it. */
  checking_category: boolean
  /** When a check last finished filing and proposing nothing, and its closing line. */
  category_checked_at: string | null
  category_check_note: string
  /** The assistant run that last decided this row's category; null if none or filed by hand. */
  category_check_run_id: Uuid | null
}

/** Everything a category check can change on a row, patched into the cache rather than refetched. */
export interface CategoryCheckRow {
  transaction_id: Uuid
  checking: boolean
  category_id: Uuid | null
  category_checked_at: string | null
  category_check_note: string
  category_check_run_id: Uuid | null
  suggestion: Suggestion | null
}

export interface CategoryCheckProgress {
  total: number
  done: number
  rows: CategoryCheckRow[]
}

/** A projection of the pending card; applying it still goes through `/assistant-actions/{action_id}/apply`. */
export interface Suggestion {
  action_id: Uuid
  conversation_id: Uuid
  /** The automation run behind it; null for a proposal made in the chat. */
  run_id: Uuid | null
  tool: string
  summary: string
  /** What an `update_transaction` proposal would file the row under. */
  category_id: Uuid | null
  /** A `split_transaction` proposal's allocations, in order. Empty on anything else. */
  splits: SuggestionSplit[]
  created_at: string
}

export interface SuggestionSplit {
  amount: Money
  category_id: Uuid | null
  memo: string
}

export interface DateWindow {
  from: IsoDate | null
  to: IsoDate | null
  date_field: string
}

export interface TransactionPage {
  items: Transaction[]
  /** Rows matching the query, not rows on this page. */
  count: number
  /** A partly kept split row counts only its kept splits, as reports do. */
  total: Money
  /** The same rows at their whole amounts. */
  full_total: Money
  /** Rows that count differently in the two totals; zero shows one figure. */
  partial_count: number
  /**
   * Padding rows the query matched and the page hid (`padding: 'hide'`), and
   * their sum; `total` plus this is what every matching row nets to.
   */
  padding_count: number
  padding_total: Money
  window: DateWindow
  limit: number
  offset: number
}

/** Everything a person may edit. `statement_name` is deliberately absent. */
export interface TransactionUpdate {
  account_id?: Uuid
  date?: IsoDate
  effective_date?: IsoDate | null
  amount?: string
  /** Omitted, an account move re-derives it from the new account; sent, it is pinned. */
  currency?: string
  payee?: string
  notes?: string | null
  check_number?: string | null
  category_id?: Uuid | null
  is_pending?: boolean
  is_reviewed?: boolean
  excluded_from_reports?: boolean
  excluded_from_spending_plan?: boolean
  is_bill?: boolean
  is_subscription?: boolean
  user_flag?: string | null
  user_flag_note?: string | null
  receipt_not_needed?: boolean
  tag_ids?: Uuid[]
  /** Replaces the row's allocations in the same request; they must sum to the amount. */
  splits?: SplitWrite[]
}

/** Amounts leave as strings: a float in the body loses precision before validation. */
export interface SplitWrite {
  amount: string
  category_id: Uuid | null
  memo: string | null
  tag_ids: Uuid[]
}

export interface TransactionCreate {
  account_id: Uuid
  date: IsoDate
  /** The reporting date. Null files the row under `date`. */
  effective_date?: IsoDate | null
  amount: string
  /** Omitted means the account's own currency. */
  currency?: string
  /** Settable once, at ingest. */
  statement_name?: string
  payee?: string
  notes?: string | null
  check_number?: string | null
  category_id?: Uuid | null
  is_pending?: boolean
  is_reviewed?: boolean
  excluded_from_reports?: boolean
  excluded_from_spending_plan?: boolean
  user_flag?: string | null
  user_flag_note?: string | null
  tag_ids?: Uuid[]
  splits?: SplitWrite[]
}

export interface BulkReviewResult {
  updated: number
  window: DateWindow
}

export interface AccountBalances {
  balance: Money
  balance_with_pending: Money
  available_balance: Money
  /** Null when there is no credit limit. Render an em dash, never zero. */
  credit_used_pct: string | null
}

/** Where an account's statement figures came from: a filed bill. */
export interface StatementSource {
  bill_id: string
  source: 'provider' | 'email' | 'manual' | 'assistant'
  provider: string
  issued_on: IsoDate | null
  due_on: IsoDate
  fetched_at: string
}

export interface Account {
  id: Uuid
  name: string
  description: string | null
  notes: string | null
  kind: AccountKind
  type: string
  currency: string
  institution_id: Uuid | null
  /** Null is a manual account. */
  connection_id: Uuid | null
  masked_number: string | null
  /** Resolved by the server: the custom logo, else the provider's favicon. */
  logo_url: string | null
  /** Null means "use the provider's", which is not the same as an empty string. */
  custom_logo_url: string | null
  sort_order: number

  provider_balance: Money | null
  provider_balance_at: string | null
  /** A zero the sync declined to apply on an established account; `provider_balance`
   *  keeps the last good figure. Null when nothing is held. */
  withheld_balance: Money | null
  withheld_balance_at: string | null
  /** What the held figure was compared with: the last balance plus the
   *  transactions since, and what they came to. Empty when nothing is held. */
  withheld_balance_reason: string
  /** Turns the withheld-zero guard off. */
  accept_zero_balance: boolean
  opening_balance: Money
  opening_balance_on: IsoDate | null
  goal_balance: Money
  pending_holds: Money

  credit_limit: Money | null
  /** SimpleFIN has no field for these: they come from a connector, a bill or the
   *  account dialog. Null means the header leaves it out, not that it draws a dash. */
  statement_balance: Money | null
  minimum_due: Money | null
  due_date: IsoDate | null
  /** Null unless the statement figures were copied from a bill. */
  statement_source?: StatementSource | null
  interest_rate: string | null
  statement_close_day: number | null
  /** Untyped on purpose: the provider's shape. Absent on a manual account. */
  provider_extra?: Record<string, unknown>

  excluded_from_reports: boolean
  excluded_from_spending_plan: boolean
  excluded_from_account_bar: boolean
  include_in_net_worth: boolean
  exclude_bank_pending: boolean
  /** Every spending row needs a receipt: the household's setting, or by default for an HSA's cash account. */
  requires_receipts: boolean

  is_closed: boolean
  closed_on: IsoDate | null
  simplefin_account_id: string | null
  sync_floor_on: IsoDate | null
  /** Null is automatic; the day in force is on the listing's `history`. */
  history_starts_on: IsoDate | null
  /** Null follows the institution's threshold; zero always shows it. */
  hide_below_balance: Money | null
  /** The register tab the account opens on; null opens on the rows. */
  default_register_tab: RegisterTab | null

  /** All null on an account that is not a house or a car. */
  property_address: string | null
  vehicle_vin: string | null
  vehicle_mileage: number | null
  vehicle_mileage_as_of: IsoDate | null
  vehicle_miles_per_year: number | null
  valuation_source: string | null
  valued_at: string | null

  secured_by_account_id: Uuid | null
}

export interface AccountEquity {
  value: Money
  /** Positive, unlike the loans' stored negative balances. */
  owed: Money
  equity: Money
  /** A plain ratio (`0.6762` for 67.62%); null for an asset worth nothing. */
  loan_to_value: string | null
  loan_ids: Uuid[]
}

/** Where an account's balance history begins (calculations.md §4). */
export interface AccountHistoryStart {
  starts_on: IsoDate | null
  automatic_starts_on: IsoDate | null
}

/** The tabs over a register: the rows, and the Spending and Income charts. */
export type RegisterTab = 'all' | 'spending' | 'income'

export interface AccountWithBalances extends Account {
  balances: AccountBalances
  /** Present only on an asset that secures at least one loan. */
  equity: AccountEquity | null
  /** On the listing only: a PATCH answers with the row alone. */
  history?: AccountHistoryStart
  /** On the listing only. Hides the row from account lists and from the
   *  pickers that file something into an account; totals, reports and
   *  filters still count it. */
  hidden_small_balance?: boolean
}

export interface AccountWindowSummary {
  account_id: Uuid
  window: DateWindow
  opening_balance: Money
  ending_balance: Money
  total: Money
  count: number
  balances: AccountBalances
}

export interface Category {
  id: Uuid
  parent_id: Uuid | null
  name: string
  kind: CategoryKind
  known_category_id: string | null
  txf_id: string | null
  txf_ids: string[]
  is_user_assignable: boolean
  is_editable: boolean
  /** Why the category cannot be deleted, or null when it can. */
  protected_reason: string | null
  excluded_from_reports: boolean
  excluded_from_spending_plan: boolean
  excluded_from_category_list: boolean
  sort_order: number
}

export interface Tag {
  id: Uuid
  name: string
  color: string | null
}

/**
 * `domain.FilterField`, member for member, kept to what `filterFieldIsEvaluable`
 * accepts: an unknown field fails its item, so a filter would quietly match less.
 */
export type FilterField =
  | 'category'
  | 'payee'
  | 'tag'
  | 'account'
  | 'flag'
  | 'text'
  | 'amount'
  | 'date'
  | 'is_bill_or_subscription'
  | 'is_excluded_from_reports'
  | 'is_excluded_from_spending_plan'
  | 'is_reviewed'
  | 'statement_name'
  | 'is_pending'
  | 'is_uncategorized'
  | 'is_category_undetermined'
  | 'has_category_suggestion'
  | 'has_tag'
  | 'has_attachment'
  | 'is_missing_receipt'

export type FilterOperator =
  | 'in'
  | 'contains'
  | 'is_exactly'
  | 'equals'
  | 'between'
  | 'greater_than'
  | 'less_than'
  | 'is_true'
  /** A regular expression; refused at save time if it will not compile. */
  | 'matches'

export type FilterScope =
  | 'envelope'
  | 'watchlist'
  | 'report'
  | 'saved_view'
  | 'rule'
  | 'guidance'
  | 'automation'
  | 'ad_hoc'

export interface FilterItemWrite {
  field: FilterField
  operator: FilterOperator
  group_index: number
  position: number
  /** On this item alone: an excluded three-category item excludes all three. */
  negated: boolean
  value_ids: Uuid[]
  value_texts: string[]
  text: string | null
  amount_min: string | null
  amount_max: string | null
  date_from: IsoDate | null
  date_to: IsoDate | null
  /** A relative token, stored rather than the dates it resolves to today. */
  date_preset: string | null
  state: boolean | null
}

export interface FilterWrite {
  name: string | null
  scope: FilterScope
  /** What the user typed, so the box repopulates with that and not with a re-render of the parse. */
  query_text: string | null
  /** Where a person arranged it among its scope's filters; ties keep creation order. */
  position?: number
  items: FilterItemWrite[]
}

export interface FilterRead {
  id: Uuid
  name: string | null
  scope: FilterScope
  query_text: string | null
  position?: number
  items: (FilterItemWrite & { id: Uuid })[]
}
