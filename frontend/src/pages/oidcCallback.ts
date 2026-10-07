import type { AuthValue } from '@/contexts/auth'

/**
 * Where the callback goes next, or null while the session is still settling.
 *
 * A fragment token that `/auth/me` refuses is cleared by the API client, so
 * the session lands on `anonymous` with the memoized `token` still non-null;
 * the last arm sends that to the login screen rather than spinning forever.
 * Before adoption, `anonymous` is just the mount state. A session that failed
 * to confirm for any other reason leaves the same way.
 */
export function callbackTarget(
  token: string | null,
  status: AuthValue['status'],
  adopted: boolean,
): '/login' | '/' | null {
  if (!token) return '/login'
  if (status === 'authenticated') return '/'
  if (adopted && (status === 'anonymous' || status === 'error')) return '/login'
  return null
}
