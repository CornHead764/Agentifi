import { describe, expect, it } from 'vitest'

import { category } from '@/test/builders'

import { DEFAULT_QUERY, registerParams } from './api'
import {
  EMPTY_DRAFT,
  EMPTY_UNIVERSE,
  activeFacetCount,
  amountBound,
  expandCategories,
  mergeDrafts,
  toFilterItems,
  withoutFacets,
  type FilterDraft,
  type FilterUniverse,
} from './filter'
import { parseSearch } from './search'
import type { Tag } from './types'

const AUTO = category('auto', 'Auto & Transport')
const GAS = category('gas', 'Gas & Fuel', 'auto')
const DINING = category('dining', 'Dining & Drinks')
const TAGS: Tag[] = [{ id: 'tag-1', name: 'reimbursable', color: null }]

const UNIVERSE: FilterUniverse = { categories: [AUTO, GAS, DINING], tags: TAGS }

function draft(overrides: Partial<FilterDraft>): FilterDraft {
  return { ...EMPTY_DRAFT, ...overrides }
}

describe('toFilterItems', () => {
  it('produces nothing for an empty draft, so the register asks for no filter', () => {
    expect(toFilterItems(EMPTY_DRAFT, UNIVERSE)).toEqual([])
  })

  it('includes a chosen parent category and its children', () => {
    const items = toFilterItems(draft({ categories: { ids: ['auto'], negated: false } }), UNIVERSE)

    expect(items).toHaveLength(1)
    expect(items[0].field).toBe('category')
    expect(items[0].value_ids.sort()).toEqual(['auto', 'gas'])
  })

  it('carries negation on the item, not on the values', () => {
    const items = toFilterItems(
      draft({ categories: { ids: ['dining'], negated: true } }),
      UNIVERSE,
    )

    expect(items[0].negated).toBe(true)
    expect(items[0].value_ids).toEqual(['dining'])
  })

  it('encodes "uncategorized" as its own field, not a list of every category', () => {
    // A list of every category would miss one added later.
    const items = toFilterItems(draft({ uncategorized: true }), UNIVERSE)

    expect(items).toHaveLength(1)
    expect(items[0]).toMatchObject({ field: 'is_uncategorized', state: true, negated: false })
    expect(items[0].value_ids).toEqual([])
  })

  it('encodes "has any tag" and "has no tag" over the whole tag set', () => {
    expect(toFilterItems(draft({ hasTags: true }), UNIVERSE)[0]).toMatchObject({
      field: 'tag',
      negated: false,
      value_ids: ['tag-1'],
    })
    expect(toFilterItems(draft({ hasTags: false }), UNIVERSE)[0]).toMatchObject({
      field: 'tag',
      negated: true,
    })
  })

  it('leaves the universe-derived facets out when nothing is loaded', () => {
    expect(toFilterItems(draft({ hasTags: true }), EMPTY_UNIVERSE)).toEqual([])
  })

  it('keeps "uncategorized" when no category has loaded yet', () => {
    expect(toFilterItems(draft({ uncategorized: true }), EMPTY_UNIVERSE)).toMatchObject([
      { field: 'is_uncategorized', state: true },
    ])
  })

  it('sends payees as names and the advanced pairs as states', () => {
    const items = toFilterItems(
      draft({
        payees: { values: ['Walmart'], negated: false },
        excludedFromReports: false,
        isReviewed: true,
      }),
      UNIVERSE,
    )

    expect(items.map((item) => item.field)).toEqual([
      'payee',
      'is_excluded_from_reports',
      'is_reviewed',
    ])
    expect(items[0].value_texts).toEqual(['Walmart'])
    expect(items[1].state).toBe(false)
    expect(items[2].state).toBe(true)
  })

  it('keeps a date preset alongside the window it resolved to', () => {
    const items = toFilterItems(
      draft({ date: { from: '2026-08-01', to: '2026-08-21', preset: 'this-month' } }),
      UNIVERSE,
    )

    expect(items[0]).toMatchObject({
      field: 'date',
      date_from: '2026-08-01',
      date_to: '2026-08-21',
      date_preset: 'this-month',
    })
  })

  it('picks the amount operator from which bounds are set', () => {
    const only = (facet: { min: string | null; max: string | null }) =>
      toFilterItems(draft({ amount: facet }), UNIVERSE)[0].operator

    expect(only({ min: '100.00', max: null })).toBe('greater_than')
    expect(only({ min: null, max: '100.00' })).toBe('less_than')
    expect(only({ min: '50.00', max: '200.00' })).toBe('between')
  })

  it('puts every item in group 0, so the filter is a conjunction', () => {
    const items = toFilterItems(
      draft({
        categories: { ids: ['dining'], negated: false },
        texts: ['coffee'],
        isReviewed: false,
      }),
      UNIVERSE,
    )

    expect(items.every((item) => item.group_index === 0)).toBe(true)
    expect(items.map((item) => item.position)).toEqual([0, 1, 2])
  })
})

describe('withoutFacets', () => {
  it('clears every facet and keeps the free text and the window', () => {
    const window = { from: '2026-01-01', to: '2026-01-31', preset: null }
    const cleared = withoutFacets(
      draft({
        categories: { ids: ['dining'], negated: true },
        payees: { values: ['Corner Store'], negated: false },
        isReviewed: false,
        amount: { operator: 'greater_than', min: '10.00', max: null },
        texts: ['coffee'],
        date: window,
      }),
    )
    expect(cleared).toEqual(draft({ texts: ['coffee'], date: window }))
    expect(activeFacetCount(cleared)).toBe(0)
  })
})

describe('panel and search together', () => {
  it('narrows by both', () => {
    const panel = draft({ accounts: { ids: ['acct-1'], negated: false } })
    const search = parseSearch('coffee is:reviewed', {
      universe: UNIVERSE,
      today: new Date(2026, 7, 21),
    })
    const items = toFilterItems(mergeDrafts(panel, search.draft), UNIVERSE)

    expect(items.map((item) => item.field)).toEqual(['account', 'text', 'is_reviewed'])
    expect(items[1].text).toBe('coffee')
    expect(items[1].operator).toBe('contains')
  })

  it('lets the search box override an Advanced radio', () => {
    const merged = mergeDrafts(draft({ isReviewed: false }), draft({ isReviewed: true }))
    expect(merged.isReviewed).toBe(true)
  })

  it('counts the facets the panel badge reports', () => {
    expect(activeFacetCount(EMPTY_DRAFT)).toBe(0)
    expect(
      activeFacetCount(
        draft({
          categories: { ids: ['dining'], negated: false },
          accounts: { ids: ['a'], negated: false },
          isReviewed: true,
        }),
      ),
    ).toBe(3)
    // Advanced is one facet, however many of its questions are answered.
    expect(activeFacetCount(draft({ hasAttachment: false }))).toBe(1)
    expect(activeFacetCount(draft({ hasAttachment: true, isReviewed: true }))).toBe(1)
    expect(activeFacetCount(draft({ missingReceipt: true }))).toBe(1)
  })

  it('takes the receipt answer from the search box when it has one', () => {
    expect(mergeDrafts(draft({ missingReceipt: true }), draft({})).missingReceipt).toBe(true)
    expect(
      mergeDrafts(draft({ missingReceipt: true }), draft({ missingReceipt: false })).missingReceipt,
    ).toBe(false)
  })

  it('takes the attachment answer from the search box when it has one', () => {
    expect(mergeDrafts(draft({ hasAttachment: true }), draft({})).hasAttachment).toBe(true)
    expect(
      mergeDrafts(draft({ hasAttachment: true }), draft({ hasAttachment: false })).hasAttachment,
    ).toBe(false)
  })
})

describe('registerParams', () => {
  it('always names the date field, so no one has to guess which date was filtered', () => {
    expect(registerParams(DEFAULT_QUERY)).toContain('date_field=posted')
    expect(registerParams({ ...DEFAULT_QUERY, dateField: 'effective' })).toContain(
      'date_field=effective',
    )
  })

  it('repeats account_id and omits a bound that was not set', () => {
    const params = registerParams({
      ...DEFAULT_QUERY,
      accountIds: ['a1', 'a2'],
      from: '2026-08-01',
    })

    expect(params).toContain('account_id=a1&account_id=a2')
    expect(params).toContain('from=2026-08-01')
    expect(params).not.toContain('to=')
  })

  it('tells an empty account selection apart from no selection at all', () => {
    // No account_id is every account; one empty account_id is none of them.
    expect(registerParams({ ...DEFAULT_QUERY, accountIds: null })).not.toContain('account_id')
    expect(registerParams({ ...DEFAULT_QUERY, accountIds: [] })).toContain('account_id=')
  })

  it('sends reviewed only when the review queue asked for it', () => {
    expect(registerParams(DEFAULT_QUERY)).not.toContain('reviewed=')
    expect(registerParams({ ...DEFAULT_QUERY, reviewed: false })).toContain('reviewed=false')
  })

  it('is identical for the list and the bulk action, which is why they cannot disagree', () => {
    const query = { ...DEFAULT_QUERY, reviewed: false, filterId: 'f1' }
    expect(registerParams(query)).toBe(registerParams({ ...query }))
    expect(registerParams(query)).toContain('filter_id=f1')
  })
})

describe('the three terms the client could not encode', () => {
  it('sends pending as a state item the evaluator knows', () => {
    const items = toFilterItems(draft({ isPending: true }), UNIVERSE)
    expect(items).toHaveLength(1)
    expect(items[0].field).toBe('is_pending')
    expect(items[0].operator).toBe('is_true')
    expect(items[0].state).toBe(true)
    expect(toFilterItems(draft({ isPending: false }), UNIVERSE)[0].state).toBe(false)
  })

  it('sends the sign of an expense term beside its size', () => {
    const items = toFilterItems(
      draft({ amount: { min: '500.00', max: null, direction: 'expense' } }),
      UNIVERSE,
    )
    expect(items[0].field).toBe('amount')
    expect(items[0].amount_min).toBe('500.00')
    // false is expenses; null is either sign.
    expect(items[0].state).toBe(false)
  })

  it('leaves a signless amount term matching either direction', () => {
    const items = toFilterItems(draft({ amount: { min: '500.00', max: null } }), UNIVERSE)
    expect(items[0].state).toBeNull()
  })

  it('sends the payee operator the term meant', () => {
    const typed = toFilterItems(
      draft({ payees: { values: ['walmart'], negated: false, operator: 'contains' } }),
      UNIVERSE,
    )
    expect(typed[0].field).toBe('payee')
    expect(typed[0].operator).toBe('contains')

    // The panel's picks are set membership, not a substring search.
    const picked = toFilterItems(
      draft({ payees: { values: ['Walmart'], negated: false } }),
      UNIVERSE,
    )
    expect(picked[0].operator).toBe('in')
  })

  it('carries a typed substring through the merge with a panel selection', () => {
    const merged = mergeDrafts(
      draft({ payees: { values: ['Target'], negated: false } }),
      draft({ payees: { values: ['walmart'], negated: false, operator: 'contains' } }),
    )
    expect(merged.payees.operator).toBe('contains')
    expect(merged.payees.values).toEqual(['Target', 'walmart'])
    expect(merged.isPending).toBeNull()
  })

  it('encodes a whole search string end to end', () => {
    const { draft: parsed, limitations } = parseSearch('is:pending expense>500 payee:walmart')
    expect(limitations).toEqual([])
    const fields = toFilterItems(parsed, UNIVERSE).map((one) => `${one.field}:${one.operator}`)
    expect(fields).toContain('is_pending:is_true')
    expect(fields).toContain('amount:greater_than')
    expect(fields).toContain('payee:contains')
  })
})

describe('amountBound', () => {
  it('sends a typed bound as its magnitude', () => {
    expect(amountBound('$1,250.5')).toBe('1250.50')
    expect(amountBound('-40')).toBe('40.00')
  })

  it('drops a blank bound and one that is not an amount', () => {
    expect(amountBound(null)).toBeNull()
    expect(amountBound('')).toBeNull()
    expect(amountBound('twelve')).toBeNull()
  })
})

describe('expandCategories', () => {
  it('adds the children of a chosen parent and leaves a leaf alone', () => {
    expect(expandCategories(['auto'], [AUTO, GAS, DINING]).sort()).toEqual(['auto', 'gas'])
    expect(expandCategories(['dining'], [AUTO, GAS, DINING])).toEqual(['dining'])
  })

  it('reaches a grandchild whichever order the list holds the levels in', () => {
    const service = category('service', 'Service & Parts', 'auto')
    const tires = category('tires', 'Tires', 'service')
    expect(expandCategories(['auto'], [tires, AUTO, GAS, service]).sort())
      .toEqual(['auto', 'gas', 'service', 'tires'])
    expect(expandCategories(['service'], [tires, AUTO, GAS, service]).sort())
      .toEqual(['service', 'tires'])
  })
})
