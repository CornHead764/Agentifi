/**
 * The register's custom quick filters: the space's `saved_view` filters, in
 * the order their positions give.
 */

import { useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api'
import { useInvalidatingMutation } from '@/lib/queryClient'
import type { FilterRead, FilterWrite, Uuid } from '@/lib/transactions/types'

export const SAVED_QUICK_FILTERS_KEY = ['filters', 'saved_view'] as const

export function useSavedQuickFilters() {
  return useQuery({
    queryKey: SAVED_QUICK_FILTERS_KEY,
    queryFn: ({ signal }) =>
      api.get<FilterRead[]>('/filters?scope=saved_view', undefined, signal),
  })
}

export function useCreateQuickFilter() {
  return useInvalidatingMutation(
    (body: FilterWrite) => api.post<FilterRead>('/filters', body),
    [SAVED_QUICK_FILTERS_KEY],
    { failure: 'Quick filter not saved' },
  )
}

export function useUpdateQuickFilter() {
  return useInvalidatingMutation(
    ({ id, body }: { id: Uuid; body: Partial<FilterWrite> }) =>
      api.patch<FilterRead>(`/filters/${id}`, body),
    [SAVED_QUICK_FILTERS_KEY],
    { failure: 'Quick filter not saved' },
  )
}

/** Several positions in one go, the list read again once they have all landed. */
export function useMoveQuickFilter() {
  return useInvalidatingMutation(
    (moves: { id: Uuid; position: number }[]) =>
      Promise.all(moves.map(({ id, position }) => api.patch<FilterRead>(`/filters/${id}`, { position }))),
    [SAVED_QUICK_FILTERS_KEY],
    { failure: 'Not moved' },
  )
}

export function useDeleteQuickFilter() {
  return useInvalidatingMutation(
    (id: Uuid) => api.delete<void>(`/filters/${id}`),
    [SAVED_QUICK_FILTERS_KEY],
    { failure: 'Quick filter not deleted' },
  )
}
