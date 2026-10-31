import { createContext, useContext } from 'react'

import { activeSpaceId, setActiveSpace } from '@/lib/activeSpace'

export interface SpaceValue {
  /** Null for "whichever the server defaults to". */
  activeSpaceId: string | null
  /** Persists the choice and drops the old space's cache. The caller navigates. */
  setActiveSpace: (spaceId: string | null) => void
}

/**
 * The React mirror of `lib/activeSpace`. Without `<AuthProvider>` it still
 * reads the same answer, but a switch does not reset the cache.
 */
export const SpaceContext = createContext<SpaceValue>({
  get activeSpaceId() {
    return activeSpaceId()
  },
  setActiveSpace,
})

export function useActiveSpace(): SpaceValue {
  return useContext(SpaceContext)
}
