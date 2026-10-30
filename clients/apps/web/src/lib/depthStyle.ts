import type { CSSProperties } from 'react'

/* A tree's indent is a stylesheet length, not a pixel sum computed in a
   component: all a row is handed is how deep it sits, and its class turns that
   into padding on the spacing scale. */
interface DepthStyle extends CSSProperties {
  '--depth': number
}

export const depthStyle = (depth: number): DepthStyle => ({ '--depth': depth })
