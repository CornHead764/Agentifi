import type { ToastOptions } from '@/components/ui'
import { accountDisplayName } from '@/lib/accounts'
import type { Connection, SyncRun } from '@/lib/clients/connections'
import { asSentence, plural } from '@/lib/format'

/** What a running sync is doing, in a line. */
export function syncProgressLine(run: SyncRun): string {
  switch (run.phase) {
    case 'importing': {
      const name = run.account_name === null ? '' : `: ${accountDisplayName(run.account_name)}`
      const sofar = run.transactions_imported === 0 ? '' : ` · ${plural(run.transactions_imported, 'new transaction')} so far`
      return `Importing account ${run.account} of ${run.accounts}${name}${sofar}`
    }
    case 'settling':
      return `Pairing transfers and applying rules to ${plural(run.transactions_imported, 'new transaction')}`
    default:
      return 'Asking SimpleFIN for your accounts'
  }
}

/** The same in a few words, for the refresh control. */
export function syncProgressShort(run: SyncRun): string {
  switch (run.phase) {
    case 'importing':
      return `Importing ${run.account} of ${run.accounts}`
    case 'settling':
      return 'Pairing transfers'
    default:
      return 'Reaching SimpleFIN'
  }
}

/** How far through its accounts a running sync is, out of 100. */
export function syncShare(run: SyncRun): number {
  if (run.phase === 'settling') return 100
  if (run.phase !== 'importing' || run.accounts === 0) return 0
  return Math.round(((run.account - 1) / run.accounts) * 100)
}

/** What an ended run did, in a line: "12 accounts, 340 new transactions, 1 warning". */
export function syncSummary(run: SyncRun): string {
  const parts = [plural(run.accounts, 'account'), plural(run.transactions_imported, 'new transaction')]
  const warnings = run.warnings + run.balances_held
  if (warnings > 0) parts.push(plural(warnings, 'warning'))
  return parts.join(', ')
}

/**
 * The toast for a sync that has ended. `seeWhy` opens the connection, where a
 * failure and each warning are spelled out.
 */
export function syncEndedToast(connection: Connection, seeWhy: () => void): ToastOptions {
  const run = connection.sync
  const action = { label: 'See why', onSelect: seeWhy }
  if (run === null || run.state === 'skipped') {
    return {
      title: `${connection.name} was not synced`,
      description: run?.message ? asSentence(run.message) : undefined,
      tone: 'neutral',
    }
  }
  if (run.state === 'failed') {
    return {
      title: `${connection.name} did not finish syncing`,
      description: run.message ? asSentence(run.message) : undefined,
      tone: 'error',
      action,
    }
  }
  return {
    title: `Synced ${connection.name}`,
    description: `${syncSummary(run)}.`,
    tone: 'success',
    action: run.warnings + run.balances_held > 0 ? action : undefined,
  }
}

