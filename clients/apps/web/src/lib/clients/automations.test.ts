/**
 * A trigger's stored conditions survive a round trip through `FilterFacets`'
 * draft, including an account, uncategorized and not-pending together.
 */

import { describe, expect, it } from 'vitest'

import { EMPTY_DRAFT, EMPTY_UNIVERSE } from '@/lib/transactions/filter'
import type { FilterItemWrite, FilterRead, Uuid } from '@/lib/transactions/types'

import { triggerConditions, triggerDraft } from './automations'

function item(changes: Partial<FilterItemWrite>): FilterItemWrite & { id: Uuid } {
  return {
    id: `i-${changes.field ?? 'x'}` as Uuid,
    field: 'account',
    operator: 'in',
    group_index: 0,
    position: 0,
    negated: false,
    value_ids: [],
    value_texts: [],
    text: null,
    amount_min: null,
    amount_max: null,
    date_from: null,
    date_to: null,
    date_preset: null,
    state: null,
    ...changes,
  }
}

const THREE_CONDITIONS: FilterRead = {
  id: 'f' as Uuid,
  name: 'Suggest categories',
  scope: 'automation',
  query_text: null,
  items: [
    item({ field: 'account', value_ids: ['card' as Uuid] }),
    item({ field: 'is_uncategorized', operator: 'is_true', state: true, position: 1 }),
    item({ field: 'is_pending', operator: 'is_true', state: false, position: 2 }),
  ],
}

describe('a trigger filter in the editor', () => {
  it('reopens the conditions an automation carries', () => {
    const draft = triggerDraft({ filter: THREE_CONDITIONS }, EMPTY_UNIVERSE)
    expect(draft.accounts).toEqual({ ids: ['card'], negated: false })
    expect(draft.uncategorized).toBe(true)
    expect(draft.isPending).toBe(false)
  })

  it('saves them back as the same three conditions', () => {
    const draft = triggerDraft({ filter: THREE_CONDITIONS }, EMPTY_UNIVERSE)
    const { conditions } = triggerConditions('transaction_arrived', draft, EMPTY_UNIVERSE)
    expect(conditions?.map((one) => [one.field, one.state])).toEqual([
      ['is_uncategorized', true],
      ['account', null],
      ['is_pending', false],
    ])
  })

  it('opens with nothing chosen when the automation has no filter', () => {
    expect(triggerDraft({ filter: null }, EMPTY_UNIVERSE)).toEqual(EMPTY_DRAFT)
    expect(triggerDraft(undefined, EMPTY_UNIVERSE)).toEqual(EMPTY_DRAFT)
  })

  it('sends untouched facets as an empty list, which is every row', () => {
    expect(triggerConditions('transaction_arrived', EMPTY_DRAFT, EMPTY_UNIVERSE)).toEqual({
      conditions: [],
    })
  })

  it('leaves the stored filter alone for a trigger that sees no rows', () => {
    const draft = triggerDraft({ filter: THREE_CONDITIONS }, EMPTY_UNIVERSE)
    expect(triggerConditions('daily', draft, EMPTY_UNIVERSE)).toEqual({})
    expect(triggerConditions('manual', draft, EMPTY_UNIVERSE)).toEqual({})
  })
})
