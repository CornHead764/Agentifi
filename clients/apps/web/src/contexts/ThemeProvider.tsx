import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'

import { roamingPreferences, saveRoamingPreference, useRoamingPreferences } from '@/lib/preferences'
import { readStoredChoice, writeStored } from '@/lib/storage'

import {
  THEME_PREFERENCES,
  THEME_STORAGE_KEY,
  ThemeContext,
  isThemePreference,
  type ResolvedTheme,
  type ThemePreference,
} from './theme'

const DARK_QUERY = '(prefers-color-scheme: dark)'

function systemTheme(): ResolvedTheme {
  if (typeof window === 'undefined' || !window.matchMedia) return 'dark'
  return window.matchMedia(DARK_QUERY).matches ? 'dark' : 'light'
}

/**
 * This device's choice and the account it was made for, so the account's theme
 * is adopted once per sign-in and a stale `/auth/me` cannot undo a change.
 * `forUser` is null for a choice made on the login screen.
 */
interface Choice {
  value: ThemePreference
  forUser: string | null
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [choice, setChoice] = useState<Choice>(() => ({
    value: readStoredChoice(THEME_STORAGE_KEY, THEME_PREFERENCES, 'dark'),
    forUser: null,
  }))
  const [system, setSystem] = useState<ResolvedTheme>(systemTheme)

  const roaming = useRoamingPreferences()
  const adopted =
    roaming !== null && choice.forUser !== roaming.userId && isThemePreference(roaming.theme)
      ? roaming.theme
      : null
  const preference = adopted ?? choice.value

  useEffect(() => {
    if (preference !== 'system' || typeof window === 'undefined' || !window.matchMedia) return
    const query = window.matchMedia(DARK_QUERY)
    const onChange = () => setSystem(query.matches ? 'dark' : 'light')
    onChange()
    query.addEventListener('change', onChange)
    return () => query.removeEventListener('change', onChange)
  }, [preference])

  const resolved: ResolvedTheme = preference === 'system' ? system : preference

  // Written for both values rather than removed for the default: a page that
  // gains the attribute on hydration flashes.
  useEffect(() => {
    document.documentElement.setAttribute('data-theme', resolved)
  }, [resolved])

  // Adopted values are stored too, so a reload paints right before `/auth/me` answers.
  useEffect(() => {
    writeStored(THEME_STORAGE_KEY, preference)
  }, [preference])

  const setPreference = useCallback((next: ThemePreference) => {
    setChoice({ value: next, forUser: roamingPreferences()?.userId ?? null })
    saveRoamingPreference({ theme: next })
  }, [])

  const value = useMemo(
    () => ({ preference, resolved, setPreference }),
    [preference, resolved, setPreference],
  )

  return <ThemeContext value={value}>{children}</ThemeContext>
}
