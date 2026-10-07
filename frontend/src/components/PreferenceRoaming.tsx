import { useEffect } from 'react'

import { useToast } from '@/components/ui'
import { useAuth } from '@/contexts/auth'
import { fromUserRecord, onPreferenceSaveFailure, publishRoamingPreferences } from '@/lib/preferences'
import { describeApiError } from '@/lib/transactions/queries'

/**
 * Hands the account's stored preferences up to the theme and privacy
 * providers, which wrap the auth provider so signing out keeps the theme.
 * Draws nothing.
 */
export function PreferenceRoaming() {
  const { user } = useAuth()
  const { show } = useToast()

  useEffect(() => {
    publishRoamingPreferences(user === null ? null : fromUserRecord(user))
  }, [user])

  useEffect(
    () =>
      onPreferenceSaveFailure((error) =>
        show({
          title: 'That preference stayed on this device',
          description: describeApiError(error),
          tone: 'error',
        }),
      ),
    [show],
  )

  return null
}
