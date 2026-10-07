/** Ignored accounts as the settings screens list them: by institution, each with its way back. */

import { accountDisplayName, maskedNumber } from '@/lib/accounts'
import type { Connection } from '@/lib/clients/connections'
import type { Money } from '@/lib/money'

/** One ignored account, whichever list it came from, with how to put it back. */
export interface IgnoredRow {
  key: string
  name: string
  institution: string
  details: (string | null)[]
  balance?: Money
  restore: () => Promise<unknown>
}

export interface IgnoredGroup {
  institution: string
  rows: IgnoredRow[]
}

/** The rows by institution, A to Z, with the rows that name none last. */
export function groupIgnored(rows: readonly IgnoredRow[]): IgnoredGroup[] {
  const groups = new Map<string, IgnoredRow[]>()
  for (const row of rows) {
    const institution = row.institution.trim()
    groups.set(institution, [...(groups.get(institution) ?? []), row])
  }
  return [...groups]
    .map(([institution, members]) => ({ institution, rows: members }))
    .sort((a, b) => {
      if (a.institution === '' || b.institution === '') return a.institution === '' ? 1 : -1
      return a.institution.localeCompare(b.institution)
    })
}

/** Ignored at the bank: the connection skips it, so it never arrives. */
export function remoteIgnoredRows(
  connection: Pick<Connection, 'id' | 'ignored'>,
  restore: (connectionId: string, ignoredId: string) => Promise<unknown>,
  source: string | null,
): IgnoredRow[] {
  return connection.ignored.map((entry) => ({
    key: entry.id,
    name: accountDisplayName(entry.name || entry.external_id),
    institution: entry.institution,
    details: [maskedNumber(entry.masked_number), source],
    restore: () => restore(connection.id, entry.id),
  }))
}
