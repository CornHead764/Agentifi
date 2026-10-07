/** The queries and the mutations, keyed so one write moves every list showing it. */

import { keepPreviousData, useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api'
import { queryString, type IsoDate } from '../entities'

import {
  CASH_FLOW_SHAPE,
  OCCURRENCE_LIST_SHAPE,
  REFUNDS_SHAPE,
  SERIES_HISTORY_SHAPE,
  SERIES_SHAPE,
  SUGGESTION_SHAPE,
  TAB_KINDS,
  refundFromWire,
  seriesFromWire,
  suggestionFromWire,
  type CashFlow,
  type Occurrence,
  type OccurrenceList,
  type Series,
  type SeriesHistory,
  type SeriesTab,
  type Suggestion,
  type Wire,
  type WireRefundList,
} from './types'
import {
  acceptOccurrence,
  createSeries,
  deleteSeries,
  skipOccurrence,
  updateSeries,
  type AcceptEdits,
  type SeriesDraft,
  type SeriesPatch,
} from './api'
import { cashFlowKeys, invalidateOccurrenceWrites, occurrenceKeys, seriesKeys } from './keys'
import { useInvalidatingMutation } from '@/lib/queryClient'

/** Settled slots are left out unless `includeFulfilled`, which is part of the key. */
export function useOccurrences(
  from: IsoDate,
  to: IsoDate,
  options: { includeFulfilled?: boolean; enabled?: boolean; keepPrevious?: boolean } = {},
) {
  const includeFulfilled = options.includeFulfilled ?? false
  return useQuery({
    queryKey: occurrenceKeys.range(from, to, includeFulfilled),
    enabled: options.enabled ?? true,
    placeholderData: options.keepPrevious ? keepPreviousData : undefined,
    queryFn: ({ signal }) =>
      api.get<OccurrenceList>(
        `/occurrences${queryString({
          from,
          to,
          date_field: 'effective',
          include_fulfilled: includeFulfilled || undefined,
        })}`,
        OCCURRENCE_LIST_SHAPE,
        signal,
      ),
  })
}

export function useCashFlow(
  from: IsoDate,
  to: IsoDate,
  accountIds: readonly string[] | null,
  threshold?: string,
) {
  return useQuery({
    queryKey: cashFlowKeys.range(from, to, accountIds?.join(',') ?? 'all', threshold ?? '0'),
    queryFn: ({ signal }) =>
      api.get<CashFlow>(
        // A null list omits `account_id` (every account); an empty one asks for none.
        `/cash-flow${queryString({ from, to, threshold }, { account_id: accountIds })}`,
        CASH_FLOW_SHAPE,
        signal,
      ),
  })
}

export function useSeriesList(tab: SeriesTab, search: string) {
  const kind = TAB_KINDS[tab]
  return useQuery({
    queryKey: seriesKeys.list(tab, search),
    enabled: tab !== 'suggested',
    queryFn: async ({ signal }) => {
      const rows = await api.get<Wire<Series>[]>(
        `/series${queryString({
          kind,
          // A missing flag means either.
          is_active: tab === 'all_active' ? true : undefined,
          search: search || undefined,
        })}`,
        SERIES_SHAPE,
        signal,
      )
      return rows.map(seriesFromWire)
    },
    select: (rows: Series[]) => (tab === 'canceled' ? rows.filter((one) => !one.is_active) : rows),
  })
}

/** Each month's slot up to `toMonth` (`YYYY-MM`) and what paid it. */
export function useSeriesHistory(id: string | null, toMonth: string, months = 12) {
  return useQuery({
    queryKey: seriesKeys.history(id, toMonth, months),
    enabled: id !== null,
    queryFn: async ({ signal }) => {
      const raw = await api.get<Omit<SeriesHistory, 'series'> & { series: Wire<Series> }>(
        `/series/${id}/history${queryString({ to: toMonth, months })}`,
        SERIES_HISTORY_SHAPE,
        signal,
      )
      return { ...raw, series: seriesFromWire(raw.series) }
    },
  })
}

/** Every series of every kind, paused ones included; not one of the tabs. */
export function useAllSeries(search: string) {
  return useQuery({
    queryKey: seriesKeys.everything(search),
    queryFn: async ({ signal }) => {
      const rows = await api.get<Wire<Series>[]>(
        `/series${queryString({ search: search || undefined })}`,
        SERIES_SHAPE,
        signal,
      )
      return rows.map(seriesFromWire)
    },
  })
}

export function useSuggestions(enabled: boolean, dismissed = false) {
  return useQuery({
    queryKey: seriesKeys.suggestions(dismissed),
    enabled,
    queryFn: async ({ signal }) => {
      const rows = await api.get<Wire<Suggestion>[]>(
        `/series/suggested${dismissed ? '?dismissed=true' : ''}`,
        SUGGESTION_SHAPE,
        signal,
      )
      return rows.map(suggestionFromWire)
    },
  })
}

export function useRefunds() {
  return useQuery({
    queryKey: seriesKeys.refunds,
    queryFn: async ({ signal }) => {
      const list = await api.get<WireRefundList>('/series/refunds', REFUNDS_SHAPE, signal)
      return {
        expected: list.expected.map(refundFromWire),
        completed: list.completed.map(refundFromWire),
      }
    },
  })
}

export function useCreateSeries() {
  return useInvalidatingMutation(
    (draft: SeriesDraft) => createSeries(draft),
    invalidateOccurrenceWrites,
    { failure: false },
  )
}

/** The editor's save, which says its failure inside the dialog. */
export function useUpdateSeries() {
  return useInvalidatingMutation(
    ({ id, patch }: { id: string; patch: SeriesPatch }) => updateSeries(id, patch),
    invalidateOccurrenceWrites,
    { failure: false },
  )
}

export function useSetSeriesActive() {
  return useInvalidatingMutation(
    ({ id, active }: { id: string; active: boolean }) => updateSeries(id, { is_active: active }),
    invalidateOccurrenceWrites,
    { failure: 'That recurring item was not changed' },
  )
}

export function useDeleteSeries(failure = 'That recurring item was not deleted') {
  return useInvalidatingMutation(
    (id: string) => deleteSeries(id),
    invalidateOccurrenceWrites,
    { failure },
  )
}

/** Mark a slot paid (or received), optionally at an edited amount, date or account. */
export function useAcceptOccurrence() {
  return useInvalidatingMutation(
    ({ occurrence, edits }: { occurrence: Occurrence; edits?: AcceptEdits }) =>
      acceptOccurrence(occurrence, edits),
    invalidateOccurrenceWrites,
    { failure: 'That reminder was not marked paid' },
  )
}

export function useSkipOccurrence() {
  return useInvalidatingMutation(
    (occurrence: Occurrence) => skipOccurrence(occurrence),
    invalidateOccurrenceWrites,
    { failure: 'That reminder was not skipped' },
  )
}
