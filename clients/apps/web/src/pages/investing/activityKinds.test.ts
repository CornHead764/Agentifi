import { describe, expect, it } from 'vitest'

import type { ActivityRow } from '@/lib/clients/investments'
import { parseMoney } from '@/lib/money'

import { activityFilters, activityLabel, filterActivity, mentionsSecurity } from './activityKinds'

function row(over: Partial<ActivityRow> & Pick<ActivityRow, 'statement_name'>): ActivityRow {
  return {
    transaction_id: over.statement_name,
    account_id: 'acct-brokerage',
    on: '2026-03-04',
    payee: 'Brokerage',
    category_id: null,
    amount: parseMoney('-100.00'),
    kind: 'buy',
    is_pending: false,
    ...over,
  }
}

describe('naming investment activity on screen', () => {
  it('names the kinds the server recognized', () => {
    expect(activityLabel('dividend')).toBe('Dividend')
    expect(activityLabel('reinvestment')).toBe('Reinvestment')
  })

  it('gives an unrecognized row no name at all', () => {
    // Money out of a brokerage is four different things. The cell stays empty
    // rather than picking one.
    expect(activityLabel('unknown')).toBeNull()
  })

  it('offers chips only for the kinds the window actually holds', () => {
    const chips = activityFilters([
      { kind: 'dividend', count: 3 },
      { kind: 'unknown', count: 1 },
    ])
    expect(chips.map((chip) => chip.label)).toEqual(['Dividend', 'Unnamed'])
    expect(chips[0].count).toBe(3)
  })
})

describe('narrowing the activity list', () => {
  const rows = [
    row({ statement_name: 'YOU BOUGHT ACME', kind: 'buy' }),
    row({ statement_name: 'DIVIDEND RECEIVED', kind: 'dividend', payee: 'Acme Corp' }),
    row({ statement_name: 'WIRE 8841', kind: 'unknown' }),
  ]

  it('shows everything when nothing is selected', () => {
    expect(filterActivity(rows, [], '')).toHaveLength(3)
  })

  it('keeps only the chosen kinds', () => {
    expect(filterActivity(rows, ['dividend', 'unknown'], '').map((one) => one.kind)).toEqual([
      'dividend',
      'unknown',
    ])
  })

  it('searches the bank’s wording as well as the payee', () => {
    expect(filterActivity(rows, [], 'wire')).toHaveLength(1)
    expect(filterActivity(rows, [], 'acme corp')).toHaveLength(1)
  })

  it('applies the search inside the chosen kinds rather than instead of them', () => {
    expect(filterActivity(rows, ['buy'], 'wire')).toHaveLength(0)
  })
})

describe('which rows look like they are about one security', () => {
  it('matches the ticker as a whole word', () => {
    expect(
      mentionsSecurity(row({ statement_name: 'YOU BOUGHT AAA 10 @ 100' }), 'AAA', 'Acme Corp'),
    ).toBe(true)
    // A three-letter ticker is inside a great many words, and AAAPL is a
    // different instrument.
    expect(mentionsSecurity(row({ statement_name: 'BOUGHT AAAPL' }), 'AAA', 'Acme Corp')).toBe(
      false,
    )
  })

  it('matches a name long enough to be a phrase', () => {
    expect(
      mentionsSecurity(
        row({ statement_name: '', payee: 'Acme Corp dividend' }),
        'AAA',
        'Acme Corp',
      ),
    ).toBe(true)
  })

  it('does not treat a two-letter name as evidence', () => {
    expect(mentionsSecurity(row({ statement_name: 'WIRE 8841' }), 'AAA', 'Ax')).toBe(false)
  })
})
