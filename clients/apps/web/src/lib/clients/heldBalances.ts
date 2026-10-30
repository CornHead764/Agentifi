/**
 * Held balances: a sync declined a suspicious zero and kept the last-known
 * balance. The held state is read off the accounts (`withheld_balance`), so
 * there is no query here.
 */

import { api } from '@/lib/api'
import { useInvalidatingMutation } from '@/lib/queryClient'
import { ACCOUNTS_KEY } from '@/lib/transactions/cache'
import type { Uuid } from '@/lib/transactions/types'

/** The zero becomes the balance and the guard is switched off for this account. */
export function useAcceptHeldBalance() {
  return useInvalidatingMutation(
    (id: Uuid) => api.post<void>(`/held-balances/${id}/accept`),
    [ACCOUNTS_KEY],
    { failure: 'That balance was not applied' },
  )
}
