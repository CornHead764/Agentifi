import { readStoredFlag, writeStoredFlag } from '@/lib/storage'

/** Whether an accounts-drawer row is collapsed. Open is the default; each row stays however it was left. */
function groupCollapsedKey(id: string): string {
  return `accounts.collapsed.${id}`
}

/** As toggled in this session, else as stored. */
export function groupCollapsed(toggled: ReadonlyMap<string, boolean>, id: string): boolean {
  return toggled.get(id) ?? readStoredFlag(groupCollapsedKey(id), false)
}

/** `toggled` with the row flipped, and the new state stored. */
export function toggleGroupCollapsed(
  toggled: ReadonlyMap<string, boolean>,
  id: string,
): Map<string, boolean> {
  const collapsed = !groupCollapsed(toggled, id)
  writeStoredFlag(groupCollapsedKey(id), collapsed)
  return new Map(toggled).set(id, collapsed)
}
