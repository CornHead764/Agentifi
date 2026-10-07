import { skipToken, useQuery, type QueryKey } from '@tanstack/react-query'

/**
 * Follows the pull a sign-in starts until the login says it stopped pulling.
 * The key must be the sign-in's session, outside the connector's own tree: a
 * row cached from before the sign-in also says "not pulling". Null `fetch`
 * waits for a session.
 */
export function usePullAfterSignIn<T extends { pulling: boolean }>(
  queryKey: QueryKey,
  fetch: ((signal: AbortSignal) => Promise<T>) | null,
) {
  return useQuery({
    queryKey,
    queryFn: fetch === null ? skipToken : ({ signal }) => fetch(signal),
    // A refetch on focus must not turn a finished answer back into "fetching".
    refetchInterval: (query) => (query.state.data?.pulling === false ? false : 2_000),
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    staleTime: 0,
  })
}
