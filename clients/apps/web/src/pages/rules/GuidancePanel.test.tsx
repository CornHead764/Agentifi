/**
 * The guidance tab against a seeded cache. What must be visible without
 * opening anything: each note's words in full, which transactions it is read
 * on, and whether it is switched on.
 */

import { describe, expect, it } from 'vitest'

import type { Guidance } from '@/lib/clients/guidance'
import type { Uuid } from '@/lib/transactions/types'
import { renderScreen } from '@/test/renderScreen'

import { GuidancePanel } from './GuidancePanel'

const FUEL_STOP: Guidance = {
  id: '00000000-0000-0000-0000-0000000000g1' as Uuid,
  name: 'Fuel Stop',
  instruction:
    'One charge on the day under $20 is convenience-store food — Fast Food. Over $20 is a tank ' +
    'of fuel — Gas & Fuel. Two on the same day: the larger is fuel, the smaller is food.',
  filter_id: '00000000-0000-0000-0000-0000000000f1' as Uuid,
  filter: {
    id: '00000000-0000-0000-0000-0000000000f1' as Uuid,
    name: 'Fuel Stop',
    scope: 'guidance',
    query_text: null,
    items: [
      {
        id: '00000000-0000-0000-0000-0000000000i1' as Uuid,
        field: 'statement_name',
        operator: 'contains',
        group_index: 0,
        position: 0,
        negated: false,
        value_ids: [],
        value_texts: ['fuel stop'],
        text: null,
        amount_min: null,
        amount_max: null,
        date_from: null,
        date_to: null,
        date_preset: null,
        state: null,
      },
    ],
  },
  owns_filter: true,
  applies_to: 'the statement name contains "fuel stop"',
  is_active: true,
  position: 0,
}

const SHARED_FILTER: Guidance = {
  id: '00000000-0000-0000-0000-0000000000g2' as Uuid,
  name: 'Costco',
  instruction: 'Warehouse runs are Groceries unless the receipt says otherwise.',
  filter_id: '00000000-0000-0000-0000-0000000000f2' as Uuid,
  filter: { ...FUEL_STOP.filter, id: '00000000-0000-0000-0000-0000000000f2' as Uuid, scope: 'saved' },
  owns_filter: false,
  applies_to: 'the payee is one of "Costco"',
  is_active: false,
  position: 1,
}

function render(notes: Guidance[]): string {
  return renderScreen(<GuidancePanel creating={false} onCreatingChange={() => {}} />, {
    seed: [
      [['guidance'], notes],
      [['accounts'], []],
    ],
  })
}

describe('the guidance tab', () => {
  it('shows each note, with the rows it will be read on', () => {
    const markup = render([FUEL_STOP, SHARED_FILTER])
    expect(markup).toContain('Fuel Stop')
    expect(markup).toContain('the larger is fuel, the smaller is food')
    // The filter, summarised by `describeConditions` — the same rendering the
    // rules list uses, rather than a second sentence about the same clauses.
    expect(markup).toContain('Statement name contains fuel stop')
  })

  it('is drawn as the rules list, not a shape of its own', () => {
    // The two lists sit one tab apart now. The columns are where that shows.
    const markup = render([FUEL_STOP, SHARED_FILTER])
    expect(markup).toContain('Order')
    expect(markup).toContain('If a transaction matches')
    expect(markup).toContain('Tell the assistant')
    expect(markup).toContain('Deactivate Fuel Stop')
    expect(markup).toContain('Move Fuel Stop later')
    expect(markup).toContain('Actions for Fuel Stop')
  })

  it('shows a switched-off note as off rather than hiding it', () => {
    // Switching a note off is how somebody stops it without retyping it, so
    // the row has to stay visible and say which state it is in.
    const markup = render([FUEL_STOP, SHARED_FILTER])
    expect(markup).toContain('rule-row--off')
    expect(markup).toContain('Activate Costco')
    expect(markup).toContain('Warehouse runs are Groceries')
  })

  it('says what a note is for when there are none', () => {
    const markup = render([])
    expect(markup).toContain('No guidance yet')
    expect(markup).toContain('Fuel Stop')
    expect(markup).not.toContain('rule-order')
  })
})
