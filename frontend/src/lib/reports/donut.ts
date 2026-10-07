import type { ReportNode } from '@/lib/clients/reports'

/**
 * The level worth drawing a donut of: descend while a level holds exactly one
 * node, so a report rooted under a single "Expenses" node shows its categories.
 */
export function sliceableLevel(groups: readonly ReportNode[]): readonly ReportNode[] {
  let level = groups
  while (level.length === 1 && level[0].children.length > 0) level = level[0].children
  return level
}

/**
 * One step down the group tree. The engine already answered with the whole
 * tree, so descending is a walk, not a re-run. The path is re-resolved against
 * the data every time, since a re-run can remove a key; an unresolved step
 * ends the walk.
 */
export interface DonutLevel {
  nodes: readonly ReportNode[]
  /** The nodes stepped through to reach them, outermost first. */
  trail: readonly ReportNode[]
}

export function drillTo(groups: readonly ReportNode[], path: readonly string[]): DonutLevel {
  let nodes = sliceableLevel(groups)
  const trail: ReportNode[] = []

  for (const key of path) {
    const step = nodes.find((node) => node.key === key)
    if (step === undefined || step.children.length === 0) break
    trail.push(step)
    nodes = step.children
  }

  return { nodes, trail }
}

export function trailKeys(level: DonutLevel): string[] {
  return level.trail.map((node) => node.key)
}
