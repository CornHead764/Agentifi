/**
 * Turning what a person typed into a request body. `statement_name` is never on
 * an update (ground rule 5).
 */

import type { Account, Transaction, TransactionUpdate, Uuid } from './types'
export class ImmutableFieldError extends Error {
  readonly field: string

  constructor(field: string) {
    super(
      `${field} is the bank's wording and cannot be edited; edit the payee instead`,
    )
    this.name = 'ImmutableFieldError'
    this.field = field
  }
}

export const STATEMENT_NAME_REASON =
  'The statement name is what the bank sent. Rules and recurring items match on it, so it stays as-is — rename the Payee instead.'

const IMMUTABLE = ['statement_name', 'id', 'source', 'balance', 'transfer_pair_id']

/** Throws rather than silently dropping a refused field. */
export function buildTransactionUpdate(patch: Record<string, unknown>): TransactionUpdate {
  for (const field of IMMUTABLE) {
    if (field in patch) throw new ImmutableFieldError(field)
  }
  const body: TransactionUpdate = {}
  if ('account_id' in patch) body.account_id = asString(patch.account_id)
  if ('date' in patch) body.date = asString(patch.date)
  if ('effective_date' in patch) body.effective_date = asNullableString(patch.effective_date)
  if ('amount' in patch) body.amount = asString(patch.amount)
  if ('currency' in patch) body.currency = asString(patch.currency)
  if ('payee' in patch) body.payee = asString(patch.payee)
  if ('notes' in patch) body.notes = asNullableString(patch.notes)
  if ('check_number' in patch) body.check_number = asNullableString(patch.check_number)
  if ('category_id' in patch) body.category_id = asNullableString(patch.category_id)
  if ('is_pending' in patch) body.is_pending = asBoolean(patch.is_pending)
  if ('is_reviewed' in patch) body.is_reviewed = asBoolean(patch.is_reviewed)
  if ('excluded_from_reports' in patch) {
    body.excluded_from_reports = asBoolean(patch.excluded_from_reports)
  }
  if ('excluded_from_spending_plan' in patch) {
    body.excluded_from_spending_plan = asBoolean(patch.excluded_from_spending_plan)
  }
  if ('is_bill' in patch) body.is_bill = asBoolean(patch.is_bill)
  if ('is_subscription' in patch) body.is_subscription = asBoolean(patch.is_subscription)
  if ('user_flag' in patch) body.user_flag = asNullableString(patch.user_flag)
  if ('user_flag_note' in patch) body.user_flag_note = asNullableString(patch.user_flag_note)
  if ('receipt_not_needed' in patch) {
    body.receipt_not_needed = asBoolean(patch.receipt_not_needed)
  }
  if ('tag_ids' in patch) body.tag_ids = asStringList(patch.tag_ids)
  return body
}

function asString(value: unknown): string {
  if (typeof value !== 'string') throw new TypeError(`expected a string, got ${typeof value}`)
  return value
}

function asNullableString(value: unknown): string | null {
  if (value === null) return null
  return asString(value)
}

function asBoolean(value: unknown): boolean {
  if (typeof value !== 'boolean') throw new TypeError(`expected a boolean, got ${typeof value}`)
  return value
}

function asStringList(value: unknown): string[] {
  if (!Array.isArray(value)) throw new TypeError('expected a list of ids')
  return value.map(asString)
}

/** Ground rule 5: the clean name, falling back to the bank's. */
export function displayPayee(txn: Pick<Transaction, 'payee' | 'statement_name'>): string {
  return txn.payee || txn.statement_name
}

/** The account's currency, else the space's primary one, else `USD` (the server's default). */
export function openingCurrency(
  accountId: Uuid | null,
  accounts: readonly Account[],
  spaceCurrency: string | null | undefined,
): string {
  const account = accounts.find((row) => row.id === accountId)
  return account?.currency || spaceCurrency || 'USD'
}
