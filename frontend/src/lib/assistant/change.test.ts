import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'
import type { Uuid } from '@/lib/transactions/types'

import { describeAbout, describeChange } from './change'

const PETS = '00000000-0000-0000-0000-0000000000c1' as Uuid
const HOUSEHOLD = '00000000-0000-0000-0000-0000000000c2' as Uuid
const SHOPPING = '00000000-0000-0000-0000-0000000000c3' as Uuid

function action(overrides: Record<string, unknown>) {
  return {
    tool: 'split_transaction',
    body: null,
    proposed_body: null,
    ...overrides,
  } as Parameters<typeof describeChange>[0]
}

describe('describing a proposed change', () => {
  it('names each part of a split with its amount, category and item', () => {
    const view = describeChange(
      action({
        body: {
          splits: [
            { amount: '-30.00', category_id: PETS, memo: 'Glass Storage Set' },
            { amount: '-40.00', category_id: HOUSEHOLD, memo: 'Batteries' },
          ],
        },
      }),
    )
    expect(view.opaque).toBe(false)
    expect(view.kind).toBe('Split into 2 parts')
    expect(view.splits.map((split) => split.categoryId)).toEqual([PETS, HOUSEHOLD])
    expect(view.splits[0].amount).toBe(moneyFromCents(-3000))
    expect(view.splits[1].memo).toBe('Batteries')
    expect(view.total).toBe(moneyFromCents(-7000))
  })

  it('keeps what the model asked for beside what was applied', () => {
    const view = describeChange(
      action({
        body: { splits: [{ amount: '-40.00', category_id: HOUSEHOLD, memo: '' }] },
        proposed_body: { splits: [{ amount: '-40.00', category_id: SHOPPING, memo: '' }] },
      }),
    )
    expect(view.splits[0].categoryId).toBe(HOUSEHOLD)
    expect(view.splits[0].proposedCategoryId).toBe(SHOPPING)
  })

  it('reads a transaction change as its category and the fields it sets', () => {
    const view = describeChange(
      action({
        tool: 'update_transaction',
        body: { category_id: PETS, payee: 'Example Pet Supply', is_reviewed: true },
      }),
    )
    expect(view.category?.categoryId).toBe(PETS)
    expect(view.details).toEqual([
      { label: 'Payee', value: 'Example Pet Supply' },
      { label: 'Mark reviewed', value: 'yes' },
    ])
  })

  it('says nothing about a category on a change that does not set one', () => {
    // An absent category_id is "not changing the category", not "file under nothing".
    const view = describeChange(action({ tool: 'update_transaction', body: { payee: 'Example Pet Supply' } }))
    expect(view.category).toBeNull()
  })

  it('falls back to the raw request for a change it cannot read', () => {
    expect(describeChange(action({ tool: 'create_rule', body: { name: 'Costco' } })).opaque).toBe(true)
    expect(describeChange(action({ tool: 'split_transaction', body: {} })).opaque).toBe(true)
  })

  it('shows an amount it cannot parse as it stands rather than as a number', () => {
    const view = describeChange(
      action({ body: { splits: [{ amount: 'about twenty', category_id: PETS }] } }),
    )
    expect(view.splits[0].amount).toBeNull()
    expect(view.splits[0].amountText).toBe('about twenty')
    expect(view.total).toBeNull()
  })
})

describe('describing what a change is about', () => {
  const about = {
    transaction_id: '00000000-0000-0000-0000-0000000000t1' as Uuid,
    account_id: '00000000-0000-0000-0000-0000000000a1' as Uuid,
    date: '2026-07-11',
    statement_name: 'AMZN MKTP US*2H41Z',
    payee: 'Amazon',
    amount: '-17.00',
  }

  it('leads with the wording the bank will still use next year', () => {
    expect(describeAbout({ about }).line).toBe('AMZN MKTP US*2H41Z · Amazon · -$17.00 · 2026-07-11')
  })

  it('does not say the same name twice', () => {
    expect(describeAbout({ about: { ...about, payee: 'amzn mktp us*2H41Z' } }).line).toBe(
      'AMZN MKTP US*2H41Z · -$17.00 · 2026-07-11',
    )
  })

  it('names what the order was for, with each share', () => {
    const view = describeAbout({
      about: { ...about, items: [{ title: 'Wooden Toy Train Set', amount: '-17.00' }] },
    })
    expect(view.items).toEqual([
      { title: 'Wooden Toy Train Set', amount: moneyFromCents(-1700) },
    ])
  })

  it('says nothing at all about a change that is not about a row', () => {
    expect(describeAbout({ about: null })).toEqual({ line: '', items: [] })
  })
})
