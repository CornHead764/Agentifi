import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'

import { roamingPreferences, saveRoamingPreference, useRoamingPreferences } from '@/lib/preferences'
import { readStoredFlag, writeStoredFlag } from '@/lib/storage'

import { PRIVACY_STORAGE_KEY, PrivacyContext } from './privacy'

/** This device's choice, and the account it was made for — see `ThemeProvider`. */
interface Choice {
  value: boolean
  forUser: string | null
}

export function PrivacyProvider({ children }: { children: ReactNode }) {
  const [choice, setChoice] = useState<Choice>(() => ({
    value: readStoredFlag(PRIVACY_STORAGE_KEY, false),
    forUser: null,
  }))

  const roaming = useRoamingPreferences()
  const hidden =
    roaming !== null && choice.forUser !== roaming.userId ? roaming.privacyMode : choice.value

  const setHidden = useCallback((next: boolean) => {
    setChoice({ value: next, forUser: roamingPreferences()?.userId ?? null })
    saveRoamingPreference({ privacy_mode: next })
  }, [])

  const toggle = useCallback(() => setHidden(!hidden), [hidden, setHidden])

  useEffect(() => {
    writeStoredFlag(PRIVACY_STORAGE_KEY, hidden)
  }, [hidden])

  // Exposed on the root so money drawn outside React (a chart axis) can honour it from CSS.
  useEffect(() => {
    document.documentElement.toggleAttribute('data-privacy', hidden)
  }, [hidden])

  const value = useMemo(() => ({ hidden, setHidden, toggle }), [hidden, setHidden, toggle])

  return <PrivacyContext value={value}>{children}</PrivacyContext>
}
