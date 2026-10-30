import { describe, expect, it } from 'vitest'

import type { AssistantAction, Conversation } from '@/lib/clients/assistant'
import { parseMoney } from '@/lib/money'
import type { Uuid } from '@/lib/transactions/types'

import { runDecision } from './runDecision'

function action(overrides: Partial<AssistantAction>): AssistantAction {
  return {
    id: 'a1' as Uuid,
    tool: 'update_transaction',
    summary: 'The one item is a toy (confidence 0.9)',
    method: 'PATCH',
    path: '/transactions/t1',
    body: { category_id: 'toys' },
    status: 'pending',
    result: '',
    status_code: 0,
    created_at: '2026-10-07T12:00:00Z',
    decided_at: null,
    about: {
      transaction_id: 't1' as Uuid,
      account_id: 'acct' as Uuid,
      date: '2026-10-06',
      statement_name: 'EXAMPLE SHOP PMTS',
      payee: 'Amazon',
      amount: '-12.00',
      items: [{ title: 'Wooden blocks' }],
    },
    preview: {
      resource: 'transactions',
      verb: 'update',
      fields: [],
      names: { toys: 'Toys' },
      basis: 'order_items',
    },
    ...overrides,
  }
}

function conversation(actions: AssistantAction[]): Conversation {
  return { id: 'c1' as Uuid, title: '', created_at: '', updated_at: '', messages: [], actions }
}

describe("a run's decision", () => {
  it("is its category proposal, with the row's items and amount", () => {
    const decision = runDecision({ confidence: 0.9, conversation: conversation([action({})]) })

    expect(decision).toEqual({
      split: false,
      lines: [
        { categoryId: 'toys', confidence: 0.9, items: ['Wooden blocks'], amount: parseMoney('-12.00') },
      ],
      names: { toys: 'Toys' },
      basis: 'order_items',
      sentence: 'The one item is a toy',
      status: 'pending',
    })
  })

  it('lists a split part by part with each part’s own figure', () => {
    const decision = runDecision({
      confidence: 0.8,
      conversation: conversation([
        action({
          tool: 'split_transaction',
          body: {
            splits: [
              { amount: '-6.00', category_id: 'toys', memo: 'Wooden blocks, Sock puppet' },
              { amount: '-3.49', category_id: 'home', memo: '' },
            ],
          },
          preview: {
            resource: 'transactions',
            verb: 'update',
            fields: [],
            names: {},
            split_confidences: [0.95, null],
          },
        }),
      ]),
    })

    expect(decision?.split).toBe(true)
    expect(decision?.lines).toEqual([
      {
        categoryId: 'toys',
        confidence: 0.95,
        items: ['Wooden blocks', 'Sock puppet'],
        amount: parseMoney('-6.00'),
      },
      { categoryId: 'home', confidence: null, items: [], amount: parseMoney('-3.49') },
    ])
  })

  it('is nothing for a run that proposed no category', () => {
    expect(runDecision({ confidence: null, conversation: conversation([]) })).toBeNull()
    expect(
      runDecision({
        confidence: null,
        conversation: conversation([action({ body: { payee: 'Amazon' } })]),
      }),
    ).toBeNull()
  })
})
