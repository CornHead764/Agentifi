import type { ReactNode } from 'react'

/**
 * The 404 page's body, naming the route that matched nothing. It says what
 * to do instead and shows no figures.
 */
export function Placeholder({ route, children }: { route: string; children: ReactNode }) {
  return (
    <div className="page">
      <div className="placeholder">
        <p>{children}</p>
        <p className="placeholder__route">{route}</p>
      </div>
    </div>
  )
}
