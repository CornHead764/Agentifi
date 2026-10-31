import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'

import { MOTION_STORAGE_KEY, asMotionMs, motionDuration } from '@/lib/motion'
import { roamingPreferences, saveRoamingPreference, useRoamingPreferences } from '@/lib/preferences'
import { readStored, writeStored } from '@/lib/storage'

import { MotionContext } from './motion'

const REDUCE_QUERY = '(prefers-reduced-motion: reduce)'

function systemReducesMotion(): boolean {
  if (typeof window === 'undefined' || !window.matchMedia) return false
  return window.matchMedia(REDUCE_QUERY).matches
}

/** This device's choice, and the account it was made for — see `ThemeProvider`. */
interface Choice {
  value: number
  forUser: string | null
}

export function MotionProvider({ children }: { children: ReactNode }) {
  const [choice, setChoice] = useState<Choice>(() => ({
    value: asMotionMs(readStored(MOTION_STORAGE_KEY)),
    forUser: null,
  }))
  const [reduced, setReduced] = useState(systemReducesMotion)

  const roaming = useRoamingPreferences()
  const preference =
    roaming !== null && choice.forUser !== roaming.userId ? roaming.animationMs : choice.value

  useEffect(() => {
    if (typeof window === 'undefined' || !window.matchMedia) return
    const query = window.matchMedia(REDUCE_QUERY)
    const onChange = () => setReduced(query.matches)
    onChange()
    query.addEventListener('change', onChange)
    return () => query.removeEventListener('change', onChange)
  }, [])

  const duration = motionDuration(preference, reduced)

  // `tokens.css` carries the default the first paint uses before this runs.
  useEffect(() => {
    document.documentElement.style.setProperty('--motion', `${duration}ms`)
  }, [duration])

  // The preference, not the resolved duration: a reduced-motion setting that is
  // turned off again must give back the speed that was chosen, not zero.
  useEffect(() => {
    writeStored(MOTION_STORAGE_KEY, String(preference))
  }, [preference])

  const setPreference = useCallback((next: number) => {
    const ms = asMotionMs(next)
    setChoice({ value: ms, forUser: roamingPreferences()?.userId ?? null })
    saveRoamingPreference({ animation_duration_ms: ms })
  }, [])

  const value = useMemo(
    () => ({ preference, duration, setPreference }),
    [preference, duration, setPreference],
  )

  return <MotionContext value={value}>{children}</MotionContext>
}
