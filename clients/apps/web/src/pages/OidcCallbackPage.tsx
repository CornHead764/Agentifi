import { useEffect, useState } from 'react'
import { Navigate } from 'react-router-dom'

import { Spinner } from '@/components/ui'
import { useAuth } from '@/contexts/auth'

import { callbackTarget } from './oidcCallback'

/**
 * Where the provider's redirect lands, once the backend's oidcCallback has set
 * the session cookie. The page adopts that session and moves on; the URL
 * names nothing that signs anyone in.
 */
export function OidcCallbackPage() {
  const { adoptSession, status } = useAuth()
  const [adopted, setAdopted] = useState(false)

  useEffect(() => {
    void adoptSession().then(() => setAdopted(true))
  }, [adoptSession])

  const target = callbackTarget(status, adopted)
  if (target) return <Navigate to={target} replace />

  return (
    <div className="auth auth--waiting" role="status" aria-label="Signing in">
      <Spinner size={24} />
    </div>
  )
}
