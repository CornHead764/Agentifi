/**
 * Institutions. The small-balance threshold is per institution, not per
 * connection, because one SimpleFIN connection spans every linked bank. The
 * server decides what is hidden, so a write invalidates the accounts.
 */

import { useQuery } from '@tanstack/react-query'

import { api, type Money, type MoneyShape } from '@/lib/api'
import { useInvalidatingMutation } from '@/lib/queryClient'
import { ACCOUNTS_KEY } from '@/lib/transactions/cache'
import type { Uuid } from '@/lib/transactions/types'

export interface Institution {
  id: Uuid
  name: string
  logo_url: string | null
  /** Null when the institution hides nothing. */
  hide_below_balance: Money | null
}

const INSTITUTION: MoneyShape<Institution> = { hide_below_balance: 'money' }

export const INSTITUTIONS_KEY = ['institutions'] as const

export function useInstitutions() {
  return useQuery({
    queryKey: INSTITUTIONS_KEY,
    queryFn: ({ signal }) => api.get<Institution[]>('/institutions', INSTITUTION, signal),
  })
}

/** `threshold` is a 2dp wire string, or null to hide nothing. */
export function useSetInstitutionThreshold() {
  return useInvalidatingMutation(
    ({ id, threshold }: { id: Uuid; threshold: string | null }) =>
      api.patch<Institution>(`/institutions/${id}`, { hide_below_balance: threshold }, INSTITUTION),
    [INSTITUTIONS_KEY, ACCOUNTS_KEY],
  )
}
