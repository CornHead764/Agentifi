import { useMemo, useState, type ReactNode } from 'react'

import { HeaderOverrideContext } from './headerOverride'

/** Wraps the shell rather than the app: a route with no shell has no header. */
export function HeaderOverrideProvider({ children }: { children: ReactNode }) {
  const [node, setNode] = useState<ReactNode>(null)
  const value = useMemo(() => ({ node, set: setNode }), [node])

  return <HeaderOverrideContext value={value}>{children}</HeaderOverrideContext>
}
