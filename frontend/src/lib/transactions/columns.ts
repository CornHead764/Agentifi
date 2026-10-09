/**
 * Which columns the register shows, and how tall its rows are. Date, Payee and
 * Category are locked on: a row cannot be identified without them.
 */

import { readStoredChoice, readStoredFlag, writeStored, writeStoredFlag } from '@/lib/storage'

import type { RowHeight } from './rows'

export type ColumnId =
  | 'date'
  | 'account'
  | 'flag'
  | 'reviewed'
  | 'payee'
  | 'statement_name'
  | 'category'
  | 'split'
  | 'tags'
  | 'notes'
  | 'attachment'
  | 'exclusion'
  | 'transfer'
  | 'amount'
  | 'balance'
  | 'check_number'

export interface ColumnDef {
  id: ColumnId
  label: string
  /** A CSS grid track. */
  track: string
  locked?: boolean
  numeric?: boolean
  /** The label is only for the accessibility tree. */
  iconOnly?: boolean
}

/** In the register's left-to-right order. */
export const COLUMNS: readonly ColumnDef[] = [
  // A text column's floor is its widest typical string plus the cell's 22px of
  // padding and border, rounded up to 0.25rem. `fitColumns` counts only floors,
  // so a floor too low clips and one too high evicts a neighbour early.
  { id: 'date', label: 'Date', track: '7rem', locked: true },
  { id: 'account', label: 'Account', track: 'minmax(10rem, 1fr)' },
  { id: 'flag', label: 'Flag', track: '2rem', iconOnly: true },
  { id: 'reviewed', label: 'Reviewed', track: '2rem', iconOnly: true },
  { id: 'payee', label: 'Payee', track: 'minmax(12.75rem, 1.35fr)', locked: true },
  // A `LockedCell`: its lock glyph and gap take a further 21px of overhead.
  { id: 'statement_name', label: 'Statement name', track: 'minmax(18.75rem, 1.2fr)' },
  { id: 'category', label: 'Category', track: 'minmax(9.75rem, 1fr)', locked: true },
  { id: 'split', label: 'Split', track: '2rem', iconOnly: true },
  { id: 'tags', label: 'Tags', track: 'minmax(5.625rem, 0.7fr)' },
  { id: 'notes', label: 'Notes', track: 'minmax(5.625rem, 0.7fr)' },
  { id: 'attachment', label: 'Attachment', track: '2rem', iconOnly: true },
  { id: 'exclusion', label: 'Exclusion', track: '4rem' },
  { id: 'transfer', label: 'Transfer', track: '2rem', iconOnly: true },
  // Sized for a signed six-figure balance: a clipped amount is a wrong number on screen.
  { id: 'amount', label: 'Amount', track: '7.75rem', numeric: true },
  { id: 'balance', label: 'Balance', track: '7.75rem', numeric: true },
  { id: 'check_number', label: 'Check #', track: '4.375rem', numeric: true },
]

const DEFAULT_HIDDEN: readonly ColumnId[] = ['transfer', 'check_number', 'split']

/** The Spending and Income tabs' activity table. */
export const ACTIVITY_COLUMNS: readonly ColumnId[] = [
  'date',
  'account',
  'payee',
  'category',
  'exclusion',
  'amount',
]

/**
 * Must match `register.css`'s phone breakpoint. `rem` in a media query is the
 * browser's initial 16px, not the root's 125%, so this is 480px.
 */
export const NARROW_QUERY = '(max-width: 30rem)'

/** What the register shows on a phone, whatever the gear menu says; the preferences are ignored, not written. */
export const NARROW_COLUMNS: readonly ColumnId[] = ['date', 'payee', 'category', 'amount']

/**
 * Relies on `register.css` cutting cell padding to 0.5rem at this breakpoint
 * and `RegisterCell` dropping the year from the date. The text columns floor at
 * zero: a minimum is what would push the grid past the screen edge.
 */
const NARROW_TRACKS: Partial<Record<ColumnId, string>> = {
  date: '3.5rem',
  payee: 'minmax(0, 1.2fr)',
  category: 'minmax(0, 1fr)',
  amount: '6rem',
}

export interface ColumnPrefs {
  visible: Record<ColumnId, boolean>
  height: RowHeight
}

const HEIGHTS: readonly RowHeight[] = ['sm', 'md', 'lg']

export function defaultPrefs(): ColumnPrefs {
  return {
    visible: fromEntries(COLUMNS.map((column) => [column.id, !DEFAULT_HIDDEN.includes(column.id)])),
    height: 'md',
  }
}

export function loadColumnPrefs(): ColumnPrefs {
  const fallback = defaultPrefs()
  return {
    visible: fromEntries(
      COLUMNS.map((column) => [
        column.id,
        column.locked === true || readStoredFlag(`register.col.${column.id}`, fallback.visible[column.id]),
      ]),
    ),
    height: readStoredChoice('register.rowHeight', HEIGHTS, fallback.height),
  }
}

export function saveColumnPrefs(prefs: ColumnPrefs): void {
  for (const column of COLUMNS) {
    writeStoredFlag(`register.col.${column.id}`, prefs.visible[column.id])
  }
  writeStored('register.rowHeight', prefs.height)
}

export function visibleColumns(
  prefs: ColumnPrefs,
  only?: readonly ColumnId[],
  /** A running balance exists per account (calculations.md), so it is dropped across several. */
  scope: 'one-account' | 'many-accounts' = 'one-account',
  narrow = false,
): readonly ColumnDef[] {
  return COLUMNS.filter(
    (column) =>
      (narrow ? NARROW_COLUMNS.includes(column.id) : prefs.visible[column.id]) &&
      (only === undefined || only.includes(column.id)) &&
      !(column.id === 'balance' && scope === 'many-accounts'),
  ).map((column) => {
    const track = narrow ? NARROW_TRACKS[column.id] : undefined
    return track === undefined ? column : { ...column, track }
  })
}

export function customizableColumns(only?: readonly ColumnId[]): readonly ColumnDef[] {
  return only === undefined ? COLUMNS : COLUMNS.filter((column) => only.includes(column.id))
}

/** Why a switched-on column is not on screen; `shown` is what the register reported it drew. */
export function hiddenReason(id: ColumnId, shown: readonly ColumnId[]): string | null {
  if (shown.includes(id)) return null
  return id === 'balance' ? 'Only inside one account' : 'Hidden at this width'
}

/** The `grid-template-columns` the header and every row share. */
export function gridTemplate(columns: readonly ColumnDef[]): string {
  return [...columns.map((column) => column.track), '2rem'].join(' ')
}

/** Least load-bearing first. Date, payee, category and amount never yield. */
const YIELD_ORDER: readonly ColumnId[] = [
  'notes',
  'tags',
  'statement_name',
  'check_number',
  'exclusion',
  'transfer',
  'attachment',
  'split',
  'account',
  'reviewed',
  'flag',
  'balance',
]

export function trackMinimumRem(track: string): number {
  const match = /^(?:minmax\(\s*)?([\d.]+)rem/.exec(track)
  return match ? Number.parseFloat(match[1]) : 0
}

/**
 * Drops columns in `YIELD_ORDER` until the floors fit, so the grid never
 * scrolls sideways; preferences are untouched. `extraRem` is the row-menu track
 * and, when showing, the selection checkbox.
 */
export function fitColumns(
  columns: readonly ColumnDef[],
  widthRem: number,
  extraRem = 2,
): readonly ColumnDef[] {
  let kept = [...columns]
  for (const id of YIELD_ORDER) {
    if (floorsRem(kept) + extraRem <= widthRem) break
    kept = kept.filter((column) => column.id !== id)
  }
  return kept.length === columns.length ? columns : kept
}

export function floorsRem(columns: readonly ColumnDef[]): number {
  return columns.reduce((sum, column) => sum + trackMinimumRem(column.track), 0)
}

/** For a register too narrow for even the phone's four columns in desktop tracks. */
export function narrowColumns(columns: readonly ColumnDef[]): readonly ColumnDef[] {
  return columns
    .filter((column) => NARROW_COLUMNS.includes(column.id))
    .map((column) => {
      const track = NARROW_TRACKS[column.id]
      return track === undefined ? column : { ...column, track }
    })
}

/** `Object.fromEntries` would lose the column union, and assertions are banned outside the money boundary. */
function fromEntries(entries: readonly (readonly [ColumnId, boolean])[]): Record<ColumnId, boolean> {
  const record: Partial<Record<ColumnId, boolean>> = {}
  for (const [id, value] of entries) record[id] = value
  const complete: Record<ColumnId, boolean> = {
    date: true,
    account: true,
    flag: true,
    reviewed: true,
    payee: true,
    statement_name: true,
    category: true,
    split: true,
    tags: true,
    notes: true,
    attachment: true,
    exclusion: true,
    transfer: true,
    amount: true,
    balance: true,
    check_number: true,
  }
  for (const column of COLUMNS) complete[column.id] = record[column.id] ?? false
  return complete
}
