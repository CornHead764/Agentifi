import { Navigate, Outlet, useLocation } from 'react-router-dom'

import { Button, Spinner } from '@/components/ui'
import { useAuth } from '@/contexts/auth'
import { ApiError } from '@/lib/api'

/**
 * Nothing renders over an unverified session. `loading` is a held blank rather
 * than a redirect, so a reload does not flash the sign-in form. A session that
 * failed to confirm gets a sentence and two ways out.
 */
export function RequireAuth() {
  const { status, error, retry, signOut } = useAuth()
  const location = useLocation()

  if (status === 'loading') {
    return (
      <div className="auth auth--waiting" role="status" aria-label="Loading">
        <Spinner size={24} />
      </div>
    )
  }

  if (status === 'error') {
    const requestId = error instanceof ApiError ? error.requestId : null
    return (
      <div className="auth">
        <div className="auth__card">
          <h1 className="auth__title">{sessionFailure(error)}</h1>
          <p className="auth__lede">
            Your session could not be confirmed, so nothing can be shown yet.
          </p>
          {requestId ? (
            <p className="auth__foot">
              Reference <code>{requestId}</code>
            </p>
          ) : null}
          <div className="auth__actions">
            <Button variant="primary" onClick={() => retry?.()}>
              Try again
            </Button>
            <Button onClick={() => void signOut()}>Sign out</Button>
          </div>
        </div>
      </div>
    )
  }

  if (status === 'anonymous') {
    // Where they were going, in state rather than a `next` query parameter: a
    // deep link's filter does not survive URL-encoding, and a `next` is an
    // open redirect waiting to happen.
    return (
      <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />
    )
  }

  return <Outlet />
}

/**
 * The one sentence for a session that would not confirm. A 403 means the
 * account is switched off; anything else is the server.
 */
function sessionFailure(error: unknown): string {
  if (error instanceof ApiError && error.status === 403) return 'Your account is inactive'
  return 'Could not reach the server'
}

/**
 * The gate for an account that still holds a password somebody else chose.
 * The API is the enforcement; this stops a shell whose every request would
 * come back 403.
 */
export function RequireOwnPassword() {
  const { user } = useAuth()
  if (user?.must_change_password) return <Navigate to="/set-password" replace />
  return <Outlet />
}
