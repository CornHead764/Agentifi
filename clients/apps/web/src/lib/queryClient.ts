import {
  MutationCache,
  QueryCache,
  QueryClient,
  useMutation,
  useQueryClient,
  type QueryKey,
} from '@tanstack/react-query'

import { ApiError } from './api'
import { authKeys } from './session'
import { TRANSACTIONS_KEY, invalidateTransactions } from './transactions/cache'

declare module '@tanstack/react-query' {
  interface Register {
    mutationMeta: {
      /**
       * The title of the toast a failed mutation shows, or false for one whose
       * caller shows the failure itself. Read by `MutationFailureToasts`.
       */
      failure?: string | false
    }
  }
}

/**
 * A 403 `password_change_required` means the password was reset mid-session.
 * The flag rides on `/auth/me`, so refreshing it lets `RequireOwnPassword`
 * redirect to /set-password.
 */
function actOnRefusal(client: QueryClient, error: unknown): void {
  if (
    error instanceof ApiError &&
    error.status === 403 &&
    error.code === 'password_change_required'
  ) {
    void client.invalidateQueries({ queryKey: authKeys.me })
  }
}

/**
 * Defaults lean toward stability: freshness comes from explicit invalidation
 * after a mutation, not refetching on focus, so a register never reshuffles
 * mid-edit.
 */
export function createQueryClient(): QueryClient {
  const client: QueryClient = new QueryClient({
    queryCache: new QueryCache({ onError: (error) => actOnRefusal(client, error) }),
    mutationCache: new MutationCache({ onError: (error) => actOnRefusal(client, error) }),
    defaultOptions: {
      queries: {
        staleTime: 5 * 60 * 1000,
        gcTime: 30 * 60 * 1000,
        refetchOnWindowFocus: false,
        refetchOnReconnect: true,
        // A 4xx will not change on a second ask; 5xx and network errors might.
        retry: (failureCount, error) => {
          if (error instanceof ApiError && error.status < 500) return false
          return failureCount < 2
        },
      },
      mutations: {
        // A retried POST that succeeded the first time duplicates a transaction.
        retry: false,
      },
    },
  })
  return client
}

export const queryClient = createQueryClient()

type Invalidator = (client: QueryClient) => void

export type Invalidates = readonly (QueryKey | Invalidator)[] | Invalidator

function sameKey(a: QueryKey, b: QueryKey): boolean {
  return a.length === b.length && a.every((part, index) => part === b[index])
}

/** `TRANSACTIONS_KEY` stands for the whole set a transaction write leaves stale, not a prefix. */
export function invalidate(client: QueryClient, invalidates: Invalidates): void {
  for (const entry of typeof invalidates === 'function' ? [invalidates] : invalidates) {
    if (typeof entry === 'function') entry(client)
    else if (sameKey(entry, TRANSACTIONS_KEY)) invalidateTransactions(client)
    else void client.invalidateQueries({ queryKey: entry })
  }
}

export interface InvalidatingOptions<TArgs, TResult> {
  /** The failure toast's title, or false when the caller shows the failure itself. */
  failure?: string | false
  onSuccess?: (result: TResult, args: TArgs) => void
  onMutate?: (args: TArgs) => unknown
}

/** Invalidates on settle, so a write that failed halfway still refetches what it may have changed. */
export function useInvalidatingMutation<TArgs = void, TResult = unknown>(
  mutationFn: (args: TArgs) => Promise<TResult>,
  invalidates: Invalidates,
  { failure, onSuccess, onMutate }: InvalidatingOptions<TArgs, TResult> = {},
) {
  const client = useQueryClient()
  return useMutation({
    mutationFn,
    meta: { failure },
    onMutate,
    onSuccess,
    onSettled: () => invalidate(client, invalidates),
  })
}
