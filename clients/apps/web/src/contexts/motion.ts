import { createContext, useContext } from 'react'

export interface MotionValue {
  /** Before the system's reduced-motion setting applies. */
  preference: number
  /** 0 whenever reduced motion is asked for. */
  duration: number
  setPreference: (ms: number) => void
}

/**
 * A context rather than the roaming store: a settings change must reach the
 * provider's copy, which adopts the account record only once.
 */
export const MotionContext = createContext<MotionValue | null>(null)

export function useMotion(): MotionValue {
  const value = useContext(MotionContext)
  if (!value) throw new Error('useMotion must be used inside <MotionProvider>')
  return value
}
