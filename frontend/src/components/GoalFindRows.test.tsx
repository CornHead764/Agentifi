/**
 * "Find rows" lists what the server suggests, with why each ranks where it
 * does, as a checklist to add in one go. Figures are invented.
 */

import { describe, expect, it } from 'vitest'

import {
  describeGoalSuggestion,
  type Goal,
  type GoalSuggestion,
  type GoalSuggestions,
} from '@/lib/goals'
import { moneyFromCents } from '@/lib/money'
import { renderScreen } from '@/test/renderScreen'

import { GoalFindRows } from './GoalFindRows'

const GOAL = { id: 'g1', name: 'Lake Trip', account_name: 'Rainy Day Savings' } as Goal

function suggestion(overrides: Partial<GoalSuggestion> = {}): GoalSuggestion {
  return {
    transaction_id: 't1',
    date: '2026-08-04',
    account_id: 'card',
    account_name: 'Trip Card',
    payee: 'Example Lodge',
    amount: moneyFromCents(-12_000),
    category_name: 'Hotel',
    matches_category: false,
    matches_payee: false,
    days_from_withdrawal: null,
    ...overrides,
  }
}

function render(data: GoalSuggestions): string {
  return renderScreen(<GoalFindRows goal={GOAL} move="spend" onMove={() => {}} />, {
    seed: [[['goals', 'g1', 'suggestions', 'spend'], data]],
  })
}

describe('find rows', () => {
  it('lists every suggestion as a box to tick, with a select-all', () => {
    const html = render({
      kind: 'spending',
      from: '2026-07-18',
      to: '2027-03-01',
      truncated: false,
      rows: [
        suggestion(),
        suggestion({ transaction_id: 't2', payee: 'Canoe Rental', amount: moneyFromCents(-4_500) }),
      ],
    })

    expect(html).toContain('Select all 2')
    expect(html).toContain('Example Lodge')
    expect(html).toContain('Canoe Rental')
    expect(html.match(/role="checkbox"/g)).toHaveLength(3)
    expect(html).toContain('Tick the rows to add')
  })

  it('says why there is nothing to search when the goal has no rows yet', () => {
    const html = render({ kind: 'spending', from: null, to: null, truncated: false, rows: [] })

    expect(html).toContain('Count a row toward this goal first')
  })

  it('says when only the best of the matches were sent', () => {
    const html = render({
      kind: 'spending',
      from: '2026-07-18',
      to: '2027-03-01',
      truncated: true,
      rows: [suggestion()],
    })

    expect(html).toContain('Showing the best 1')
  })
})

describe('describeGoalSuggestion', () => {
  it('names the account and category, then the reasons it ranks high', () => {
    const text = describeGoalSuggestion(
      suggestion({ matches_category: true, days_from_withdrawal: 3 }),
    )

    expect(text).toContain('Trip Card · Hotel · same category as its spending · 3 days from a withdrawal')
  })

  it('falls back to the payee match when the category does not match', () => {
    expect(describeGoalSuggestion(suggestion({ matches_payee: true }))).toContain(
      'same payee as its spending',
    )
  })

  it('reads a withdrawal on the same day as such', () => {
    expect(describeGoalSuggestion(suggestion({ days_from_withdrawal: 0 }))).toContain(
      'same day as a withdrawal',
    )
  })
})
