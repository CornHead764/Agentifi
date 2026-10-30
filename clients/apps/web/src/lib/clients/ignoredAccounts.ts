/**
 * Ignored accounts are taken out of everything while their history is kept, so
 * every write here invalidates every query.
 */

import { useQuery } from '@tanstack/react-query'

import { api, type Money, type MoneyShape } from '@/lib/api'
import { ZERO_MONEY } from '@/lib/money'
import { useInvalidatingMutation, type Invalidates } from '@/lib/queryClient'
import type { AccountKind, AccountWithBalances, Uuid } from '@/lib/transactions/types'

export interface IgnoredLocalAccount {
  id: Uuid
  name: string
  type: string
  kind: AccountKind
  masked_number: string | null
  connection_id: Uuid | null
  institution_id: Uuid | null
  institution: string | null
  balance: Money
  ignored_at: string
}

export interface IgnoreResult {
  account_ids: Uuid[]
}

const IGNORED_ACCOUNT: MoneyShape<IgnoredLocalAccount> = { balance: 'money' }

export const IGNORED_ACCOUNTS_KEY = ['ignored-accounts'] as const

export function useIgnoredAccounts() {
  return useQuery({
    queryKey: IGNORED_ACCOUNTS_KEY,
    queryFn: ({ signal }) =>
      api.get<IgnoredLocalAccount[]>('/ignored-accounts', IGNORED_ACCOUNT, signal),
  })
}

const EVERYTHING: Invalidates = (client) => void client.invalidateQueries()

export function useIgnoreAccounts() {
  return useInvalidatingMutation(
    (accountIds: Uuid[]) =>
      api.post<IgnoreResult>('/ignored-accounts', { account_ids: accountIds }),
    EVERYTHING,
    { failure: 'That account was not ignored' },
  )
}

/** The server chooses the accounts: balance zero to the cent. */
export function useIgnoreEmptyAccounts() {
  return useInvalidatingMutation(
    (institutionId: Uuid) =>
      api.post<IgnoreResult>('/ignored-accounts/empty', { institution_id: institutionId }),
    EVERYTHING,
    { failure: 'Those accounts were not ignored' },
  )
}

export function useUnignoreAccount() {
  return useInvalidatingMutation(
    (accountId: Uuid) => api.delete<void>(`/ignored-accounts/${accountId}`),
    EVERYTHING,
  )
}

export interface EmptyAtInstitution {
  id: Uuid
  name: string
  count: number
}

/** For the offer only; the server chooses the accounts when it is taken. */
export function emptyByInstitution(
  accounts: AccountWithBalances[],
  names: Map<Uuid, string>,
): EmptyAtInstitution[] {
  const counts = new Map<Uuid, number>()
  for (const account of accounts) {
    if (account.institution_id === null || account.balances.balance !== ZERO_MONEY) continue
    counts.set(account.institution_id, (counts.get(account.institution_id) ?? 0) + 1)
  }
  return [...counts]
    .flatMap(([id, count]) => {
      const name = names.get(id)
      return name === undefined ? [] : [{ id, name, count }]
    })
    .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name))
}
