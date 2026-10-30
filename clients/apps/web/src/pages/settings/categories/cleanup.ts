import type { PurgeRequest, PurgeResult, UnusedList } from '@/lib/clients/categories'
import { plural } from '@/lib/format'
import type { Uuid } from '@/lib/transactions/types'

/**
 * The clean-up dialog's choices, as pure functions over the unused list.
 * Everything starts ticked.
 *
 * The tree is kept whole: the server refuses a group whose subcategory stays,
 * so unticking a subcategory unticks every group above it, and ticking a group
 * ticks everything under it.
 */

export type Selection = ReadonlySet<Uuid>

export function everything(list: UnusedList): Selection {
  return new Set([...list.categories.map((row) => row.id), ...list.tags.map((row) => row.id)])
}

function ancestors(list: UnusedList, id: Uuid): Uuid[] {
  const parents = new Map(list.categories.map((row) => [row.id, row.parent_id]))
  const out: Uuid[] = []
  for (let at = parents.get(id) ?? null; at !== null && parents.has(at) && !out.includes(at); ) {
    out.push(at)
    at = parents.get(at) ?? null
  }
  return out
}

function descendants(list: UnusedList, id: Uuid): Uuid[] {
  const out: Uuid[] = []
  const queue = [id]
  while (queue.length > 0) {
    const at = queue.shift()
    for (const row of list.categories) {
      if (row.parent_id === at && !out.includes(row.id)) {
        out.push(row.id)
        queue.push(row.id)
      }
    }
  }
  return out
}

/**
 * Ticks or unticks one row, carrying the tree rule with it. `reached` is a
 * shift-click's range: every row in it goes to the state the clicked row goes to.
 */
export function toggle(
  list: UnusedList,
  selection: Selection,
  id: Uuid,
  reached: readonly Uuid[] = [id],
): Selection {
  const on = !selection.has(id)
  const next = new Set(selection)
  for (const one of reached) {
    if (next.has(one) === on) continue
    const isCategory = list.categories.some((row) => row.id === one)
    if (on) {
      next.add(one)
      if (isCategory) for (const down of descendants(list, one)) next.add(down)
    } else {
      next.delete(one)
      if (isCategory) for (const up of ancestors(list, one)) next.delete(up)
    }
  }
  return next
}

/** Ticks or unticks a whole section, categories or tags, leaving the other alone. */
export function setSection(
  list: UnusedList,
  selection: Selection,
  section: 'categories' | 'tags',
  on: boolean,
): Selection {
  const next = new Set(selection)
  for (const row of list[section]) {
    if (on) next.add(row.id)
    else next.delete(row.id)
  }
  return next
}

export interface SectionCount {
  chosen: number
  total: number
}

export function counts(
  list: UnusedList,
  selection: Selection,
): { categories: SectionCount; tags: SectionCount } {
  const count = (rows: readonly { id: Uuid }[]) => ({
    chosen: rows.filter((row) => selection.has(row.id)).length,
    total: rows.length,
  })
  return { categories: count(list.categories), tags: count(list.tags) }
}

/** The section header's tick: all, none, or some. */
export function sectionState({ chosen, total }: SectionCount): boolean | 'indeterminate' {
  if (chosen === 0) return false
  return chosen === total ? true : 'indeterminate'
}

/** What the purge sends — only ids the list offered, whatever else the set holds. */
export function purgeRequest(list: UnusedList, selection: Selection): PurgeRequest {
  return {
    category_ids: list.categories.filter((row) => selection.has(row.id)).map((row) => row.id),
    tag_ids: list.tags.filter((row) => selection.has(row.id)).map((row) => row.id),
  }
}

/** "3 categories and 1 tag", leaving out a kind with none. */
export function chosenPhrase({ categories, tags }: { categories: number; tags: number }): string {
  const parts = [
    categories > 0 ? plural(categories, 'category', 'categories') : null,
    tags > 0 ? plural(tags, 'tag', 'tags') : null,
  ].filter((part): part is string => part !== null)
  return parts.length === 0 ? 'nothing' : parts.join(' and ')
}

/** "a", "a and b", "a, b, and c" — a list somebody reads out. */
function listOf(parts: readonly string[]): string {
  return new Intl.ListFormat('en', { style: 'long', type: 'conjunction' }).format(parts)
}

/** What "unused" was checked against, in the server's words. */
export function checkedAgainst(checked: readonly string[]): string {
  return checked.length === 0 ? '' : `Checked against ${listOf(checked)}.`
}

/** The toast after a purge. */
export function purgeOutcome(result: PurgeResult): { title: string; description?: string } {
  const title = `Deleted ${chosenPhrase({
    categories: result.categories_deleted,
    tags: result.tags_deleted,
  })}`
  const notes: string[] = []
  if (result.filters_repaired > 0) {
    notes.push(
      `${plural(result.filters_repaired, 'saved filter no longer names', 'saved filters no longer name')} them`,
    )
  }
  if (result.filters_retired > 0) {
    notes.push(
      `${plural(result.filters_retired, 'old register search that named only them was', 'old register searches that named only them were')} cleared`,
    )
  }
  if (result.resuggested > 0) {
    notes.push(
      `${plural(result.resuggested, 'unreviewed transaction an automation had filed under them is', 'unreviewed transactions an automation had filed under them are')} uncategorized and asked about again`,
    )
  }
  return notes.length === 0 ? { title } : { title, description: `${listOf(notes)}.` }
}
