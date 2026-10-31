/**
 * Guidance: notes handed to the assistant verbatim on the rows a filter
 * selects. A note is words, never an action, so there is no preview. It needs
 * at least one condition: an empty clause list matches nothing.
 */

import { useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api'
import type { RuleCondition, RuleFilter } from '@/lib/clients/rules'
import { useInvalidatingMutation } from '@/lib/queryClient'
import type { Uuid } from '@/lib/transactions/types'

export interface Guidance {
  id: Uuid
  name: string
  instruction: string
  filter_id: Uuid
  filter: RuleFilter
  /** A shared filter may not be edited in place. */
  owns_filter: boolean
  /** The same sentence the model is shown. */
  applies_to: string
  is_active: boolean
  position: number
}

export interface GuidanceWrite {
  name?: string
  instruction?: string
  filter_id?: Uuid
  /** An empty list is refused. */
  conditions?: RuleCondition[]
  is_active?: boolean
}

export interface GuidanceCreate extends GuidanceWrite {
  name: string
  instruction: string
  conditions: RuleCondition[]
}

/**
 * Conditions are optional: a note on a shared filter has its conditions edited
 * under the filter itself.
 */
export type GuidanceSubmit = GuidanceWrite & { name: string; instruction: string }

const GUIDANCE_KEY = ['guidance'] as const

export function useGuidance() {
  return useQuery({ queryKey: GUIDANCE_KEY, queryFn: ({ signal }) => api.get<Guidance[]>('/guidance', undefined, signal) })
}

export function useCreateGuidance() {
  return useInvalidatingMutation(
    (body: GuidanceCreate) => api.post<Guidance>('/guidance', body),
    [GUIDANCE_KEY],
  )
}

export function useUpdateGuidance() {
  return useInvalidatingMutation(
    ({ id, patch }: { id: Uuid; patch: GuidanceWrite }) =>
      api.patch<Guidance>(`/guidance/${id}`, patch),
    [GUIDANCE_KEY],
  )
}

export function useDeleteGuidance() {
  return useInvalidatingMutation(
    (id: Uuid) => api.delete<void>(`/guidance/${id}`),
    [GUIDANCE_KEY],
    { failure: 'That guidance was not deleted' },
  )
}

/** The whole live set; partial lists are refused. */
export function useReorderGuidance() {
  return useInvalidatingMutation(
    (guidanceIds: Uuid[]) =>
      api.post<Guidance[]>('/guidance/reorder', { guidance_ids: guidanceIds }),
    [GUIDANCE_KEY],
  )
}

export function useSetGuidanceActive() {
  return useInvalidatingMutation(
    ({ id, active }: { id: Uuid; active: boolean }) =>
      api.patch<Guidance>(`/guidance/${id}`, { is_active: active }),
    [GUIDANCE_KEY],
  )
}
