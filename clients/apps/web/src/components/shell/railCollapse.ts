import { readStoredFlag, writeStoredFlag } from '@/lib/storage'

/** Whether the navigation rail is collapsed to its icons. Expanded is the default; it stays however it was left. */
const RAIL_COLLAPSED_KEY = 'rail.collapsed'

export function railStartsCollapsed(): boolean {
  return readStoredFlag(RAIL_COLLAPSED_KEY, false)
}

export function storeRailCollapsed(collapsed: boolean): void {
  writeStoredFlag(RAIL_COLLAPSED_KEY, collapsed)
}
