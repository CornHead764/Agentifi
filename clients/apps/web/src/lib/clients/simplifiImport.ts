/**
 * Importing a Simplifi export into the current, empty space. The upload is a
 * preview that writes nothing and leaves a job waiting on the server; starting
 * it writes in the background, and the status is polled until it finishes.
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback } from 'react'

import { api } from '@/lib/api'
import type { ImportWarningGroup } from '@/lib/clients/imports'

export interface SimplifiImportPreview {
  /** The waiting job; empty when there is nothing to start. */
  id: string
  /** Set only when the export holds several datasets and none was chosen. */
  datasets: string[] | null
  /** What this space is called once the import lands. */
  space_name: string
  /** Rows per table the import would write. */
  counts: Record<string, number> | null
  rows: number
  errors: string[] | null
  warnings: ImportWarningGroup[] | null
  /** Why this space cannot take the import, though the file is fine. */
  refusal: string
  /** The report `agentifi import` prints. */
  summary: string
  can_import: boolean
}

export type SimplifiImportState = 'ready' | 'running' | 'done' | 'failed'

export interface SimplifiImportStatus {
  id: string
  state: SimplifiImportState
  space_name: string
  started_at: string | null
  finished_at: string | null
  rows: number
  error: string
}

export interface SimplifiImportFiles {
  exportFile: File
  rulesFile: File | null
  dataset: string
}

/** Both import steps report their failure inside the dialog. */
export function useSimplifiImportPreview() {
  return useMutation({
    meta: { failure: false },
    mutationFn: ({ exportFile, rulesFile, dataset }: SimplifiImportFiles) => {
      const files: Record<string, File> = { file: exportFile }
      if (rulesFile) files.transaction_rules = rulesFile
      return api.uploadFiles<SimplifiImportPreview>(
        '/simplifi-import',
        files,
        dataset ? { dataset } : {},
      )
    },
  })
}

export function useStartSimplifiImport() {
  return useMutation({
    meta: { failure: false },
    mutationFn: (id: string) =>
      api.post<SimplifiImportStatus>(`/simplifi-import/${id}/start`),
  })
}

export function simplifiImportKey(id: string) {
  return ['simplifi-import', id] as const
}

/** Polls once a second while the write runs, then stops. */
export function useSimplifiImportStatus(id: string | null) {
  return useQuery({
    queryKey: simplifiImportKey(id ?? 'none'),
    enabled: id !== null,
    queryFn: ({ signal }) =>
      api.get<SimplifiImportStatus>(`/simplifi-import/${id}`, undefined, signal),
    refetchInterval: (query) =>
      query.state.data === undefined || query.state.data.state === 'running' ? 1000 : false,
  })
}

/** The space was renamed and filled, so every screen's data is stale. */
export function useInvalidateAfterImport() {
  const client = useQueryClient()
  return useCallback(
    () =>
      client.invalidateQueries({ predicate: (query) => query.queryKey[0] !== 'simplifi-import' }),
    [client],
  )
}
