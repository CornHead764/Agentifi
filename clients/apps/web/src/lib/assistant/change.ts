/**
 * A proposed change, read back in words from the same stored body the card
 * renders raw. Nothing is invented; a body this cannot read is marked opaque
 * so the card shows the JSON alone. `proposed_body` is set only when somebody
 * changed a category before applying, so the card can show both.
 */

import type { AssistantAction } from '@/lib/clients/assistant'
import { formatMoney, parseMoney, sumMoney, type Money, type MoneyFormatter } from '@/lib/money'
import type { Uuid } from '@/lib/transactions/types'
import { isPlainObject } from '@/lib/typeGuards'

export interface ChangeSplit {
  index: number
  /** Null when the amount is not one this client can read. */
  amount: Money | null
  amountText: string
  categoryId: Uuid | null
  /** What the model asked for, when somebody chose differently. */
  proposedCategoryId: Uuid | null
  memo: string
}

export interface ChangeCategory {
  categoryId: Uuid | null
  proposedCategoryId: Uuid | null
}

export interface ChangeDetail {
  label: string
  value: string
  /** `money` and `account` are formatted by the card, which can resolve them. */
  kind?: 'money' | 'account'
}

export interface ChangeView {
  kind: string
  /** Null when the change files nothing under a category. */
  category: ChangeCategory | null
  splits: ChangeSplit[]
  total: Money | null
  details: ChangeDetail[]
  /** Show the request raw and alone. */
  opaque: boolean
  editable: boolean
}

const OPAQUE: ChangeView = {
  kind: '',
  category: null,
  splits: [],
  total: null,
  details: [],
  opaque: true,
  editable: false,
}

export function describeChange(
  action: Pick<AssistantAction, 'tool' | 'body' | 'proposed_body'>,
): ChangeView {
  const body = action.body
  if (!body) return OPAQUE

  switch (action.tool) {
    case 'update_transaction':
      return {
        ...OPAQUE,
        kind: 'Change a transaction',
        category: categoryOf(body, action.proposed_body),
        details: [
          text(body, 'payee', 'Payee'),
          text(body, 'notes', 'Note'),
          flag(body, 'is_reviewed', 'Mark reviewed'),
          flag(body, 'excluded_from_reports', 'Exclude from reports'),
          flag(body, 'excluded_from_spending_plan', 'Exclude from the spending plan'),
          tags(body),
        ].filter(present),
        opaque: false,
        editable: true,
      }

    case 'create_transaction':
      return {
        ...OPAQUE,
        kind: 'Add a transaction',
        // A new row always has a category slot: absent means uncategorized.
        category: categoryOf(body, action.proposed_body, true),
        details: [
          text(body, 'date', 'Date'),
          money(body, 'amount', 'Amount'),
          account(body),
          text(body, 'payee', 'Payee'),
          text(body, 'notes', 'Note'),
        ].filter(present),
        opaque: false,
        editable: true,
      }

    case 'split_transaction': {
      const splits = readSplits(body, action.proposed_body)
      if (splits.length === 0) return OPAQUE
      return {
        ...OPAQUE,
        kind: `Split into ${splits.length} parts`,
        splits,
        total: totalOf(splits),
        opaque: false,
        editable: true,
      }
    }

    default:
      return OPAQUE
  }
}

export interface AboutView {
  /** "AMZN MKTP US · Amazon · -$17.00 · 2026-07-11". Empty when unknown. */
  line: string
  /** What the order was for. Empty for a row no order stands behind. */
  items: { title: string; amount: Money | null }[]
}

/**
 * The change's subject in one line, bank wording first (the cleaned payee only
 * when it differs), plus the matched order's items.
 */
export function describeAbout(
  action: Pick<AssistantAction, 'about'>,
  money: MoneyFormatter = formatMoney,
): AboutView {
  const about = action.about
  if (!about) return { line: '', items: [] }

  const words = [about.statement_name.trim()]
  const payee = about.payee.trim()
  if (payee !== '' && payee.toLowerCase() !== about.statement_name.trim().toLowerCase()) {
    words.push(payee)
  }
  const amount = asMoney(about.amount)
  if (amount !== null) words.push(money(amount))
  if (about.date !== '') words.push(about.date)

  return {
    line: words.filter((word) => word !== '').join(' · '),
    items: (about.items ?? []).map((item) => ({
      title: item.title,
      amount: item.amount === undefined ? null : asMoney(item.amount),
    })),
  }
}

function categoryOf(
  body: Record<string, unknown>,
  proposed: Record<string, unknown> | null | undefined,
  always = false,
): ChangeCategory | null {
  // On an existing row, an absent `category_id` means "not changing it", unlike
  // `null`, "file under nothing"; only the second is a categorization to show.
  if (!always && !('category_id' in body)) return null
  return {
    categoryId: asUuid(body.category_id),
    proposedCategoryId: proposed ? asUuid(proposed.category_id) : null,
  }
}

function readSplits(
  body: Record<string, unknown>,
  proposed: Record<string, unknown> | null | undefined,
): ChangeSplit[] {
  const rows = Array.isArray(body.splits) ? body.splits : []
  const was = proposed && Array.isArray(proposed.splits) ? proposed.splits : []
  return rows.flatMap((row, index) => {
    const one = asObject(row)
    if (one === null) return []
    const before = asObject(was[index])
    return [
      {
        index,
        amount: asMoney(one.amount),
        amountText: asText(one.amount),
        categoryId: asUuid(one.category_id),
        proposedCategoryId: before === null ? null : asUuid(before.category_id),
        memo: asText(one.memo),
      },
    ]
  })
}

function totalOf(splits: readonly ChangeSplit[]): Money | null {
  const amounts: Money[] = []
  for (const split of splits) {
    if (split.amount === null) return null
    amounts.push(split.amount)
  }
  return sumMoney(amounts)
}

function text(body: Record<string, unknown>, key: string, label: string): ChangeDetail | null {
  const value = asText(body[key])
  return value === '' ? null : { label, value }
}

function money(body: Record<string, unknown>, key: string, label: string): ChangeDetail | null {
  const value = asText(body[key])
  return value === '' ? null : { label, value, kind: 'money' }
}

function account(body: Record<string, unknown>): ChangeDetail | null {
  const value = asText(body.account_id)
  return value === '' ? null : { label: 'Account', value, kind: 'account' }
}

function flag(body: Record<string, unknown>, key: string, label: string): ChangeDetail | null {
  if (typeof body[key] !== 'boolean') return null
  return { label, value: body[key] ? 'yes' : 'no' }
}

function tags(body: Record<string, unknown>): ChangeDetail | null {
  if (!Array.isArray(body.tag_ids) || body.tag_ids.length === 0) return null
  return { label: 'Tags', value: `${body.tag_ids.length}` }
}

function present(detail: ChangeDetail | null): detail is ChangeDetail {
  return detail !== null
}

function asText(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function asUuid(value: unknown): Uuid | null {
  return typeof value === 'string' && value !== '' ? value : null
}

/** Null for anything but a JSON object, including an array a model sent for `splits`. */
function asObject(value: unknown): Record<string, unknown> | null {
  return isPlainObject(value) ? value : null
}

/** Null for an amount we cannot read: a card must not show a wrong one. */
function asMoney(value: unknown): Money | null {
  if (typeof value !== 'string') return null
  try {
    return parseMoney(value)
  } catch {
    return null
  }
}
