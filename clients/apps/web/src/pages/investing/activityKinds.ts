/**
 * What each named kind of investment activity is called on screen, and the
 * Transactions tab's filter over them.
 *
 * "Unknown" has no label on purpose: money leaving a brokerage could be any of
 * several kinds, and naming one beside a real amount invents a fact. The cell
 * stays empty and the filter calls the bucket "Unnamed".
 */

import type { ActivityKind, ActivityRow } from '@/lib/clients/investments'
import { matchesSearch } from '@/lib/search'

const LABELS: Record<Exclude<ActivityKind, 'unknown'>, string> = {
  buy: 'Buy',
  sell: 'Sell',
  dividend: 'Dividend',
  reinvestment: 'Reinvestment',
  interest: 'Interest',
  fee: 'Fee',
  contribution: 'Contribution',
  withdrawal: 'Withdrawal',
}

/** The action name, or null for a row nothing named. */
export function activityLabel(kind: ActivityKind): string | null {
  return kind === 'unknown' ? null : LABELS[kind]
}

/** The same name, with a word for the unnamed bucket a filter chip needs. */
function activityFilterLabel(kind: ActivityKind): string {
  return activityLabel(kind) ?? 'Unnamed'
}

/** The chips, built from what the window holds rather than every kind that exists. */
export function activityFilters(
  summary: readonly { kind: ActivityKind; count: number }[],
): { kind: ActivityKind; label: string; count: number }[] {
  return summary.map((one) => ({
    kind: one.kind,
    label: activityFilterLabel(one.kind),
    count: one.count,
  }))
}

export function filterActivity(
  rows: readonly ActivityRow[],
  kinds: readonly ActivityKind[],
  search: string,
): ActivityRow[] {
  return rows.filter(
    (row) =>
      (kinds.length === 0 || kinds.includes(row.kind)) &&
      matchesSearch(search, row.payee, row.statement_name),
  )
}

/**
 * Which rows look like they are about one security. Nothing in the schema
 * links a transaction to a security, so this reads the wording, and the screen
 * says so. The symbol is matched as a whole word, since a short ticker is
 * inside many words; the name only as a phrase long enough to be evidence.
 */
export function mentionsSecurity(row: ActivityRow, symbol: string, name: string): boolean {
  const text = `${row.statement_name} ${row.payee}`.toLowerCase()
  const ticker = symbol.trim().toLowerCase()
  if (ticker !== '') {
    const words = text.split(/[^a-z0-9]+/)
    if (words.includes(ticker)) return true
  }
  const label = name.trim().toLowerCase()
  return label.length >= 4 && text.includes(label)
}
