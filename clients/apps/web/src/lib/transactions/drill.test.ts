import { describe, expect, it } from 'vitest'

import { category } from '@/test/builders'

import {
  drillFacets,
  drillLabel,
  drillUnder,
  readDrill,
  stepFor,
  withDrill,
  type DrillStep,
} from './drill'
import type { Uuid } from './types'

const TRAVEL = 'aaaaaaaa-0000-4000-8000-000000000001' as Uuid
const TRANSPORT = 'aaaaaaaa-0000-4000-8000-000000000002' as Uuid

const universe = {
  categories: [
    category(TRAVEL, 'Travel'),
    category(TRANSPORT, 'Transport', TRAVEL),
  ],
  tags: [],
}

describe('the chart drill on the URL', () => {
  it('round-trips the steps in the order they were taken, keeping every other parameter', () => {
    const steps: DrillStep[] = [
      { by: 'category', key: TRAVEL },
      { by: 'category', key: TRANSPORT },
      { by: 'payee', key: 'Night Train: Sleeper' },
    ]
    const params = withDrill(new URLSearchParams('tab=spending'), steps)
    expect(params.get('tab')).toBe('spending')
    expect(readDrill(params)).toEqual(steps)
  })

  it('drops a step it cannot read rather than guessing at it', () => {
    const params = new URLSearchParams(
      `drill=category:${TRAVEL}&drill=account:x&drill=nonsense&drill=payee:&drill=category:everything-else`,
    )
    expect(readDrill(params)).toEqual([{ by: 'category', key: TRAVEL }])
  })

  it('clears to the top', () => {
    const params = withDrill(new URLSearchParams(`tab=income&drill=category:${TRAVEL}`), [])
    expect(params.toString()).toBe('tab=income')
  })
})

describe('what a drill narrows', () => {
  it('narrows to the deepest category drilled into', () => {
    expect(
      drillFacets([
        { by: 'category', key: TRAVEL },
        { by: 'category', key: TRANSPORT },
      ]),
    ).toEqual({ categories: { ids: [TRANSPORT], negated: false }, uncategorized: false })
  })

  it('keeps a category step when a payee is drilled into under it', () => {
    expect(
      drillFacets([
        { by: 'category', key: TRAVEL },
        { by: 'payee', key: 'Harbour Ferries' },
      ]),
    ).toEqual({
      categories: { ids: [TRAVEL], negated: false },
      uncategorized: false,
      payees: { values: ['Harbour Ferries'], negated: false },
    })
  })

  it('charts the children of the last category drilled into, and none for uncategorized', () => {
    expect(drillUnder([])).toBeNull()
    expect(
      drillUnder([
        { by: 'category', key: TRAVEL },
        { by: 'payee', key: 'Harbour Ferries' },
      ]),
    ).toBe(TRAVEL)
    expect(drillUnder([{ by: 'category', key: 'uncategorized' }])).toBeNull()
  })
})

describe('the step a clicked wedge takes', () => {
  it('takes a payee by the name the filter compares, not its folded key', () => {
    expect(stepFor('payee', { key: 'harbour ferries', label: 'Harbour Ferries' }, null)).toEqual({
      by: 'payee',
      key: 'Harbour Ferries',
    })
  })

  it('takes no step from the drilled category’s own line, the tail, or no grouping', () => {
    expect(stepFor('category', { key: TRAVEL, label: 'Travel' }, TRAVEL)).toBeNull()
    expect(stepFor('category', { key: 'everything-else', label: 'Everything else' }, null)).toBeNull()
    expect(stepFor('none', { key: 'all', label: 'All categories' }, null)).toBeNull()
    expect(stepFor('category', { key: TRANSPORT, label: 'Transport' }, TRAVEL)).toEqual({
      by: 'category',
      key: TRANSPORT,
    })
  })
})

describe('a step’s label', () => {
  it('names the category, the absences and the payee', () => {
    expect(drillLabel({ by: 'category', key: TRANSPORT }, universe)).toBe('Transport')
    expect(drillLabel({ by: 'category', key: 'uncategorized' }, universe)).toBe('Uncategorized')
    expect(drillLabel({ by: 'tag', key: '' }, universe)).toBe('No tag')
    expect(drillLabel({ by: 'payee', key: 'Harbour Ferries' }, universe)).toBe('Harbour Ferries')
  })
})
