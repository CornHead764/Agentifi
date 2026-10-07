import { createContext, useContext, useEffect, type ReactNode } from 'react'

export interface HeaderOverrideValue {
  node: ReactNode
  set: (node: ReactNode) => void
}

const NO_OVERRIDE: HeaderOverrideValue = { node: null, set: () => {} }

export const HeaderOverrideContext = createContext<HeaderOverrideValue>(NO_OVERRIDE)

/** Defaults to nothing rather than throwing, for renders with no provider. */
export function useHeaderOverrideNode(): ReactNode {
  return useContext(HeaderOverrideContext).node
}

/**
 * Take over the app header for as long as `node` is not null; it is handed
 * back on unmount. `node` must be memoized, or setting it loops to "maximum
 * update depth exceeded" (a mutation's `mutate` is stable; its result is not).
 */
export function useHeaderOverride(node: ReactNode): void {
  const { set } = useContext(HeaderOverrideContext)
  useEffect(() => {
    set(node)
    return () => set(null)
  }, [node, set])
}
