import { describe, expect, it } from 'vitest'

import type { ReportNode } from '@/lib/clients/reports'
import { ZERO_MONEY } from '@/lib/money'

import { drillTo, sliceableLevel, trailKeys } from './donut'

function node(key: string, children: ReportNode[] = []): ReportNode {
  return { key, label: key, depth: 0, total: ZERO_MONEY, count: 0, children, transactions: [] }
}

/** A spending report: one "Expenses" root, parents under it, leaves under those. */
const GROUPS: ReportNode[] = [
  node('expense', [
    node('food', [node('groceries'), node('restaurants')]),
    node('auto', [node('gas')]),
    node('home'),
  ]),
]

describe('the level a donut is drawn of', () => {
  it('descends past a root that is the whole report', () => {
    expect(sliceableLevel(GROUPS).map((one) => one.key)).toEqual(['food', 'auto', 'home'])
  })

  it('slices two roots where they stand, because that is the comparison', () => {
    const both = [node('income', [node('payroll')]), node('expense', [node('food')])]
    expect(sliceableLevel(both).map((one) => one.key)).toEqual(['income', 'expense'])
  })
})

describe('drilling into a slice', () => {
  it('opens on the sliceable level with nothing behind it', () => {
    const level = drillTo(GROUPS, [])
    expect(level.nodes.map((one) => one.key)).toEqual(['food', 'auto', 'home'])
    expect(level.trail).toEqual([])
  })

  it('redraws a parent category as its subcategories', () => {
    const level = drillTo(GROUPS, ['food'])
    expect(level.nodes.map((one) => one.key)).toEqual(['groceries', 'restaurants'])
    expect(trailKeys(level)).toEqual(['food'])
  })

  /** A drilled path is held across a re-run; a key no longer grouped must not draw an empty ring. */
  it('stops at the last step the report still has', () => {
    const level = drillTo(GROUPS, ['auto', 'nothing-like-this'])
    expect(level.nodes.map((one) => one.key)).toEqual(['gas'])
    expect(trailKeys(level)).toEqual(['auto'])
  })

  it('stays where it is when the first step is gone entirely', () => {
    const level = drillTo(GROUPS, ['travel'])
    expect(level.nodes.map((one) => one.key)).toEqual(['food', 'auto', 'home'])
    expect(trailKeys(level)).toEqual([])
  })

  it('refuses to descend into a node with no children', () => {
    const level = drillTo(GROUPS, ['home'])
    expect(level.nodes.map((one) => one.key)).toEqual(['food', 'auto', 'home'])
    expect(trailKeys(level)).toEqual([])
  })

  it('walks more than one step', () => {
    const deep = [
      node('expense', [
        node('food', [node('groceries', [node('costco'), node('kroger')]), node('restaurants')]),
        node('auto', [node('gas')]),
      ]),
    ]
    const level = drillTo(deep, ['food', 'groceries'])
    expect(level.nodes.map((one) => one.key)).toEqual(['costco', 'kroger'])
    expect(trailKeys(level)).toEqual(['food', 'groceries'])
  })
})
