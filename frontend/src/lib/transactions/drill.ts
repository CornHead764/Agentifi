/**
 * The Spending and Income charts' drill-down: the wedges clicked, in order,
 * held on the URL (`?drill=category:<id>`, repeated) so the browser's Back
 * steps out one level. Each step is a patch to the register's one
 * `FilterDraft`, made by `bucketFacet`, so a drilled register is filtered
 * exactly as the Filter popover would filter it (ground rule 3).
 */

import { bucketFacet, type Bucket, type GroupBy } from './aggregate'
import type { FilterDraft, FilterUniverse } from './filter'
import type { Uuid } from './types'

export type DrillDimension = Exclude<GroupBy, 'none'>

export interface DrillStep {
  by: DrillDimension
  /**
   * What `bucketFacet` narrows by: a category id or `uncategorized`, a tag id
   * or '' for no tag, and a payee's display name, not its folded key.
   */
  key: string
}

const PARAM = 'drill'

function readDimension(value: string): DrillDimension | null {
  return value === 'category' || value === 'payee' || value === 'tag' ? value : null
}

/** The steps a URL names, in the order they were taken. A malformed step is dropped. */
export function readDrill(params: URLSearchParams): DrillStep[] {
  const steps: DrillStep[] = []
  for (const raw of params.getAll(PARAM)) {
    const colon = raw.indexOf(':')
    if (colon < 0) continue
    const by = readDimension(raw.slice(0, colon))
    if (by === null) continue
    const step = { by, key: raw.slice(colon + 1) }
    if (bucketFacet(by, { key: step.key, label: step.key }) === null) continue
    steps.push(step)
  }
  return steps
}

/** The URL with its drill set to `steps`, every other parameter kept. */
export function withDrill(params: URLSearchParams, steps: readonly DrillStep[]): URLSearchParams {
  const next = new URLSearchParams(params)
  next.delete(PARAM)
  for (const step of steps) next.append(PARAM, `${step.by}:${step.key}`)
  return next
}

/**
 * The step a clicked wedge adds, or null for a wedge no facet can express
 * ("Everything else", no grouping, no payee) and for the drilled category's
 * own line, which is already the whole of what it would narrow to.
 */
export function stepFor(
  groupBy: GroupBy,
  bucket: Pick<Bucket, 'key' | 'label'>,
  under: Uuid | null,
): DrillStep | null {
  if (groupBy === 'none' || bucketFacet(groupBy, bucket) === null) return null
  if (groupBy === 'category' && bucket.key === under) return null
  return { by: groupBy, key: groupBy === 'payee' ? bucket.label : bucket.key }
}

/** The steps folded into one patch; a later step on the same facet wins, as a child narrows its parent. */
export function drillFacets(steps: readonly DrillStep[]): Partial<FilterDraft> {
  return steps.reduce<Partial<FilterDraft>>(
    (facets, step) => ({ ...facets, ...bucketFacet(step.by, { key: step.key, label: step.key }) }),
    {},
  )
}

/** The category a category chart is drilled into: the last category step that names one. */
export function drillUnder(steps: readonly DrillStep[]): Uuid | null {
  for (let index = steps.length - 1; index >= 0; index--) {
    const step = steps[index]!
    if (step.by === 'category') return step.key === 'uncategorized' ? null : step.key
  }
  return null
}

/** What a step reads as on the breadcrumb and its chip. */
export function drillLabel(step: DrillStep, universe: FilterUniverse): string {
  switch (step.by) {
    case 'category':
      if (step.key === 'uncategorized') return 'Uncategorized'
      return universe.categories.find((category) => category.id === step.key)?.name ?? 'Unknown category'
    case 'tag':
      if (step.key === '') return 'No tag'
      return universe.tags.find((tag) => tag.id === step.key)?.name ?? 'Unknown tag'
    case 'payee':
      return step.key
  }
}
