import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'
import type { Suggestion, Uuid } from '@/lib/transactions/types'
import { transaction } from '@/test/builders'
import { renderScreen } from '@/test/renderScreen'

import { SuggestionCard } from './SuggestionCard'

const SUGGESTION: Suggestion = {
  action_id: 'p1' as Uuid,
  conversation_id: 'c1' as Uuid,
  run_id: 'r1' as Uuid,
  tool: 'update_transaction',
  summary: 'Example Coffee Roasters is a coffee shop (confidence 0.82).',
  category_id: 'coffee' as Uuid,
  splits: [],
  created_at: '2026-09-04T12:00:08Z',
  confidence: 0.82,
  decided_by: 'model',
  basis: 'payee_name',
}

const names: Record<string, string> = {
  coffee: 'Coffee Shops',
  toys: 'Toys',
  home: 'Home Supplies',
}
const categoryName = (id: Uuid | null) => (id === null ? 'Uncategorized' : (names[id] ?? '?'))
const TXN = transaction({
  payee: 'Example Coffee Roasters',
  statement_name: 'EXAMPLE COFFEE ROASTERS',
  amount: moneyFromCents(-650),
})

function card(suggestion: Suggestion): string {
  return renderScreen(
    <SuggestionCard
      txn={TXN}
      suggestion={suggestion}
      categoryName={categoryName}
      onShowRun={() => {}}
    />,
  )
}

describe('the card over a suggestion', () => {
  it('shows the row\'s current category beside the proposal', () => {
    const html = renderScreen(
      <SuggestionCard
        txn={{ ...TXN, category_id: 'home' as Uuid }}
        suggestion={SUGGESTION}
        categoryName={categoryName}
      />,
    )

    expect(html).toContain('Current category')
    expect(html).toContain('Home Supplies')
    expect(html).toContain('Coffee Shops')
  })

  it('says a transfer leg with no category is a transfer', () => {
    const html = renderScreen(
      <SuggestionCard
        txn={{ ...TXN, category_id: null, transfer_pair_id: 'p' as Uuid }}
        suggestion={SUGGESTION}
        categoryName={categoryName}
      />,
    )

    expect(html).toContain('<strong>Transfer</strong>')
  })

  it('carries an approve and a discard button only when it is given the decisions', () => {
    const plain = card(SUGGESTION)
    expect(plain).not.toContain('Approve this suggestion')

    const html = renderScreen(
      <SuggestionCard
        txn={TXN}
        suggestion={SUGGESTION}
        categoryName={categoryName}
        onApprove={() => {}}
        onDiscard={() => {}}
      />,
    )
    expect(html).toContain('aria-label="Approve this suggestion"')
    expect(html).toContain('aria-label="Discard this suggestion"')
    expect(html.indexOf('suggestion-card__actions')).toBeLessThan(
      html.indexOf('suggestion-card__head'),
    )
  })

  it('disables both while a decision is in flight', () => {
    const html = renderScreen(
      <SuggestionCard
        txn={TXN}
        suggestion={SUGGESTION}
        categoryName={categoryName}
        onApprove={() => {}}
        onDiscard={() => {}}
        deciding
      />,
    )
    expect(html.match(/disabled=""/g)).toHaveLength(2)
  })

  it('names the category with its confidence and amount, and how it got there', () => {
    const html = card(SUGGESTION)

    expect(html).toContain('Suggested category')
    expect(html).toContain('Likely · 82%')
    expect(html).toContain('Coffee Shops')
    expect(html).toContain('(82%)')
    expect(html).toContain('6.50')
    expect(html).toContain('Payee name')
    expect(html).toContain('Example Coffee Roasters is a coffee shop.')
    expect(html).not.toContain('confidence 0.82')
    expect(html).toContain('See the run')
  })

  it('lists a split part by part, each with its own confidence and items', () => {
    const html = card({
      ...SUGGESTION,
      tool: 'split_transaction',
      category_id: null,
      basis: 'order_items',
      summary: 'A toy and a sponge on one order',
      splits: [
        {
          amount: moneyFromCents(-3000),
          category_id: 'toys' as Uuid,
          memo: 'Wooden blocks, Sock puppet',
          confidence: 0.9,
        },
        { amount: moneyFromCents(-2000), category_id: 'home' as Uuid, memo: '', confidence: null },
      ],
    })

    expect(html).toContain('Suggested split')
    expect(html).toContain('2 categories')
    expect(html).toContain('Toys')
    expect(html).toContain('(90%)')
    expect(html).toContain('Wooden blocks')
    expect(html).toContain('Sock puppet')
    expect(html).toContain('Home Supplies')
    expect(html).toContain('30.00')
    expect(html).toContain('Order items')
  })

  it('says when no confidence was recorded, and leaves out a run it does not have', () => {
    const html = card({
      ...SUGGESTION,
      run_id: null,
      confidence: null,
      decided_by: '',
      basis: '',
      summary: 'Move it to Coffee Shops',
    })

    expect(html).toContain('No confidence recorded')
    expect(html).not.toContain('See the run')
    expect(html).toContain('From a conversation')
    expect(html).toContain('Proposed in a conversation')
  })
})
