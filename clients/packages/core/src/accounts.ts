/** An account number as its last four digits, "····1234", or null when there is none. */
export function maskedNumber(mask: string | null | undefined): string | null {
  const trimmed = mask?.trim() ?? ''
  if (trimmed === '') return null
  const digits = trimmed.replace(/\D/g, '')
  return `····${digits === '' ? trimmed : digits.slice(-4)}`
}

/** A bank's own id in brackets after a name, "(0a1b2c3d-…)", whole or cut short. */
const TRAILING_ID = /\s*\([0-9a-f]{8}-[0-9a-f]{4}[^)]*\)\s*$/i

/**
 * A name as a bank feed sent it, made fit to read: without the feed's id
 * after it, and said once when the bank joined two copies of it ("Auto
 * Loan Auto Loan"). A short number in brackets, "(1234)", is kept: it is
 * how the bank tells two accounts apart.
 */
export function accountDisplayName(name: string): string {
  const bare = name.replace(TRAILING_ID, '').trim()
  const words = bare.split(/\s+/)
  const half = words.length / 2
  if (Number.isInteger(half) && half > 0) {
    const first = words.slice(0, half).join(' ')
    if (first.toLowerCase() === words.slice(half).join(' ').toLowerCase()) return first
  }
  return bare === '' ? name.trim() : bare
}

/**
 * An account's name by id. An id missing from a loaded list reads "Unknown
 * account"; before the list loads every name is blank, so nothing flashes it.
 */
export function accountNamer(
  accounts: readonly { id: string; name: string }[] | undefined,
): (id: string) => string {
  if (accounts === undefined) return () => ''
  const byId = new Map(accounts.map((account) => [account.id, account.name]))
  return (id) => byId.get(id) ?? 'Unknown account'
}
