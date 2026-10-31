import { useEffect, useMemo, useState } from 'react'
import { Navigate } from 'react-router-dom'

import { Spinner } from '@/components/ui'
import { useAuth } from '@/contexts/auth'

import { callbackTarget } from './oidcCallback'

/**
 * Where the provider's redirect lands. The token arrives in the URL fragment,
 * which is not sent to servers or written to access logs (see the backend's
 * oidcCallback). It is read once, adopted, and removed from the address bar
 * so a shared URL carries no live session.
 */
export function OidcCallbackPage() {
  const { adoptToken, status } = useAuth()
  const [adopted, setAdopted] = useState(false)

  // Read during render: the fragment is already in the document, and an
  // effect would render the spinner first.
  const token = useMemo(
    () => new URLSearchParams(window.location.hash.replace(/^#/, '')).get('access_token'),
    [],
  )

  useEffect(() => {
    if (!token) return
    window.history.replaceState(null, '', '/')
    void adoptToken(token).then(() => setAdopted(true))
  }, [token, adoptToken])

  const target = callbackTarget(token, status, adopted)
  if (target) return <Navigate to={target} replace />

  return (
    <div className="auth auth--waiting" role="status" aria-label="Signing in">
      <Spinner size={24} />
    </div>
  )
}
