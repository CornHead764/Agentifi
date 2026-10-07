import { createContext, useContext } from 'react'

export type ThemePreference = 'light' | 'dark' | 'system'
export type ResolvedTheme = 'light' | 'dark'

export const THEME_PREFERENCES: readonly ThemePreference[] = ['light', 'dark', 'system']
export const THEME_STORAGE_KEY = 'theme'

export interface ThemeValue {
  preference: ThemePreference
  resolved: ResolvedTheme
  setPreference: (preference: ThemePreference) => void
}

/** Guards against an older or newer install: an unknown value adopted here would be stored for good. */
export function isThemePreference(value: string): value is ThemePreference {
  return THEME_PREFERENCES.some((preference) => preference === value)
}

export const ThemeContext = createContext<ThemeValue | null>(null)

export function useTheme(): ThemeValue {
  const value = useContext(ThemeContext)
  if (!value) throw new Error('useTheme must be used inside <ThemeProvider>')
  return value
}
