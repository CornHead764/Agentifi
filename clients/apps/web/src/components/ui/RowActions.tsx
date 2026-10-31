import type { ReactNode } from 'react'

/**
 * The buttons at the end of a table or list row, `size="sm"`: one line,
 * centred on the row, right-aligned.
 */
export function RowActions({ children }: { children: ReactNode }) {
  return <span className="row-actions">{children}</span>
}
