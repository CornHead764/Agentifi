/**
 * The one row a link pointed at: `/rules?rule=<id>`. The id stays in the URL,
 * so a reload lands on the same row; the editor opens once, until `settle`.
 */

import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'

export interface LinkedItem {
  linked: string | null
  /** The same id until the editor it opened has been closed, then null. */
  opening: string | null
  settle: () => void
}

export function useLinkedItem(param: string): LinkedItem {
  const [params] = useSearchParams()
  const linked = params.get(param)
  const [settled, setSettled] = useState<string | null>(null)
  return {
    linked,
    opening: linked !== null && linked !== settled ? linked : null,
    settle: () => setSettled(linked),
  }
}

/** `ready` is whether the list has loaded; before then there is no row. */
export function useScrollToLinked(linked: string | null, ready: boolean) {
  useEffect(() => {
    if (linked === null || !ready) return
    const row = document.querySelector(`[data-linked="true"]`)
    row?.scrollIntoView?.({ block: 'center' })
  }, [linked, ready])
}
