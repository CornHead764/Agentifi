import type { AuthValue } from '@/contexts/auth'

/**
 * Where the callback goes next, or null while the session is still settling.
 *
 * Before adoption, `anonymous` is just the mount state. A cookie that
 * `/auth/me` refuses signs the browser out again, so after adoption
 * `anonymous` goes to the login screen rather than spinning forever, as does
 * a session that failed to confirm for any other reason.
 */
export function callbackTarget(
  status: AuthValue['status'],
  adopted: boolean,
): '/login' | '/' | null {
  if (status === 'authenticated') return '/'
  if (adopted && (status === 'anonymous' || status === 'error')) return '/login'
  return null
}
