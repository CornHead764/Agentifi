/**
 * Importing a statement file: uploaded once as a dry-run preview and again to
 * commit, so the server holds no state between the two.
 */

import { useMutation, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api'
import { invalidateTransactions } from '@/lib/transactions/cache'

export interface ImportWritten {
  accounts: number
  categories: number
  tags: number
  transactions: number
  transactions_skipped: number
}

/**
 * Every warning of one kind, as both importers report them: a one-line
 * summary, how many records it covers, and each line beneath it. A note is
 * something the file holds that the importer does not read.
 */
export interface ImportWarningGroup {
  kind: string
  note: boolean
  summary: string
  /** Where the app resolves this kind, or empty. */
  action: string
  count: number
  items: string[]
}

export interface ImportResult {
  /** Decided from the file's content, not its name. */
  format: 'ofx' | 'csv'
  dry_run: boolean
  summary: string
  accounts: string[]
  transactions: number
  errors: string[]
  warnings: ImportWarningGroup[]
  /** Null on a preview. */
  written: ImportWritten | null
}

function importFile(file: File, account: string, dryRun: boolean) {
  const fields: Record<string, string> = {}
  if (account.trim() !== '') fields.account = account.trim()
  if (dryRun) fields.dry_run = 'true'
  return api.upload<ImportResult>('/imports', file, fields)
}

/** The card reports its own failures, beside the file they are about. */
export function useImportFile() {
  const client = useQueryClient()
  return useMutation({
    meta: { failure: false },
    mutationFn: ({ file, account, dryRun }: { file: File; account: string; dryRun: boolean }) =>
      importFile(file, account, dryRun),
    onSuccess: (result) => {
      if (!result.dry_run) invalidateTransactions(client)
    },
  })
}
