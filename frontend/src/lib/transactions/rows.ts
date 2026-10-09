/**
 * The flat row list the virtualizer walks: group headings are rows too. Order comes from the server and is never re-sorted here, except
 * that pending rows are lifted into their own group at the top.
 */

import { sumMoney, type Money } from '@/lib/money'

import { formatDate } from '@/lib/format'
import type { Transaction } from './types'

export type RowHeight = 'sm' | 'md' | 'lg'

/**
 * The token each density's row is sized by, in `styles/tokens.css`: the ones
 * `Table density` reads, so a register row and a table row are one height.
 * The grid reads them with `tokenPx`; nothing here restates their values.
 */
export const ROW_HEIGHT_TOKENS: Record<RowHeight, string> = {
  sm: '--row-height-sm',
  md: '--row-height',
  lg: '--row-height-lg',
}

/**
 * A phone's two-line row height before `RegisterGrid` measures it, at the
 * design scale. Close enough that the scrollbar does not jump.
 */
export const NARROW_ROW_TWO_LINE = 62

export type RegisterRow =
  | {
      kind: 'group'
      key: string
      section: string
      label: string
      count: number
      total: Money
      collapsed: boolean
    }
  | { kind: 'transaction'; key: string; txn: Transaction }

export interface BuildRowsOptions {
  /** Closed sections. Their members are left out of the array, not hidden, so they take no scroll space and cannot be selected. */
  collapsed?: ReadonlySet<string>
}

/** The section a transaction is filed under. Pending first, then by month. */
const PENDING_SECTION = 'pending'

export function sectionKey(txn: Transaction): string {
  return txn.is_pending ? PENDING_SECTION : txn.date.slice(0, 7)
}

export function buildRows(
  transactions: readonly Transaction[],
  options: BuildRowsOptions,
): RegisterRow[] {
  const pending = transactions.filter((txn) => txn.is_pending)
  const posted = transactions.filter((txn) => !txn.is_pending)

  const rows: RegisterRow[] = []
  if (pending.length > 0) rows.push(...group(PENDING_SECTION, 'Pending', pending, options))

  let month: string | null = null
  let bucket: Transaction[] = []
  for (const txn of posted) {
    const key = sectionKey(txn)
    if (key !== month) {
      if (month !== null) rows.push(...group(month, monthLabel(month), bucket, options))
      month = key
      bucket = []
    }
    bucket.push(txn)
  }
  if (month !== null) rows.push(...group(month, monthLabel(month), bucket, options))

  return rows
}

function group(
  key: string,
  label: string,
  members: readonly Transaction[],
  options: BuildRowsOptions,
): RegisterRow[] {
  const collapsed = options.collapsed?.has(key) === true
  const rows: RegisterRow[] = [
    {
      kind: 'group',
      key: `group:${key}`,
      section: key,
      label,
      count: members.length,
      // Summed from every member, so a closed section still shows its total.
      total: sumMoney(members.map((txn) => txn.amount)),
      collapsed,
    },
  ]
  if (collapsed) return rows
  for (const txn of members) {
    rows.push({ kind: 'transaction', key: txn.id, txn })
  }
  return rows
}

/** `2026-08` as `August 2026`, without constructing a timestamp. */
function monthLabel(key: string): string {
  return formatDate(`${key}-01`, 'monthLong')
}

/**
 * What the virtualizer must be told a row occupies. `densityPx` is the
 * density's token already in device pixels (`tokenPx`), and every kind of row
 * takes it, so a heading keeps the register's rhythm.
 * `minHeight` is in device pixels and applies to transaction rows only.
 */
export function rowHeight(row: RegisterRow, densityPx: number, minHeight = 0): number {
  if (row.kind !== 'transaction') return densityPx
  return Math.max(densityPx, minHeight)
}
