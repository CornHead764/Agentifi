import { describe, expect, it } from 'vitest'

import { setDisplayLocale } from '@/lib/locale'
import { moneyFromCents } from '@/lib/money'
import { transaction } from '@/test/builders'
import {
  buildRows,
  NARROW_ROW_TWO_LINE,
  rowHeight,
  sectionKey,
} from './rows'
import type { Transaction, Uuid } from './types'

function txn(overrides: Partial<Transaction>): Transaction {
  return transaction({
    id: '00000000-0000-0000-0000-000000000001',
    account_id: '00000000-0000-0000-0000-0000000000a1',
    date: '2026-08-15',
    amount: moneyFromCents(-1_000),
    statement_name: 'STORE 123',
    payee: 'Store',
    ...overrides,
  })
}

describe('the register rows', () => {
  it('buckets pending first, then months, each led by its group row', () => {
    const rows = buildRows(
      [
        txn({ id: 'p1' as Uuid, is_pending: true, date: '2026-08-29' }),
        txn({ id: 't1' as Uuid, date: '2026-08-15' }),
        txn({ id: 't2' as Uuid, date: '2026-07-02' }),
      ],
      { showSplits: false },
    )

    expect(rows.map((row) => row.kind)).toEqual([
      'group',
      'transaction',
      'group',
      'transaction',
      'group',
      'transaction',
    ])
    expect(rows[0]).toMatchObject({ label: 'Pending', count: 1 })
    expect(rows[2]).toMatchObject({ label: 'August 2026' })
    expect(rows[4]).toMatchObject({ label: 'July 2026' })
  })

  it('sums each group from its own members', () => {
    const rows = buildRows(
      [
        txn({ id: 't1' as Uuid, amount: moneyFromCents(-2_500) }),
        txn({ id: 't2' as Uuid, amount: moneyFromCents(-1_500) }),
      ],
      { showSplits: false },
    )
    expect(rows[0]).toMatchObject({ kind: 'group', total: moneyFromCents(-4_000) })
  })

  it('leaves a closed section holding nothing but its own heading', () => {
    const rows = buildRows(
      [
        txn({ id: 'p1' as Uuid, is_pending: true, date: '2026-08-29' }),
        txn({ id: 't1' as Uuid, date: '2026-08-15', amount: moneyFromCents(-2_500) }),
        txn({ id: 't2' as Uuid, date: '2026-08-02', amount: moneyFromCents(-1_500) }),
      ],
      { showSplits: false, collapsed: new Set(['2026-08']) },
    )

    expect(rows.map((row) => row.kind)).toEqual(['group', 'transaction', 'group'])
    expect(rows[2]).toMatchObject({
      kind: 'group',
      section: '2026-08',
      count: 2,
      total: moneyFromCents(-4_000),
      collapsed: true,
    })
  })

  it('closes the pending section by the name the pending rows carry', () => {
    const rows = buildRows([txn({ id: 'p1' as Uuid, is_pending: true })], {
      showSplits: false,
      collapsed: new Set([sectionKey(txn({ is_pending: true }))]),
    })
    expect(rows.map((row) => row.kind)).toEqual(['group'])
  })

  it('keeps the split rows of a closed section out of the list as well', () => {
    const split = {
      id: 's1' as Uuid,
      position: 0,
      category_id: null,
      amount: moneyFromCents(-500),
      memo: null,
      tag_ids: [],
    }
    const rows = buildRows([txn({ id: 't1' as Uuid, splits: [split] })], {
      showSplits: true,
      collapsed: new Set(['2026-08']),
    })
    expect(rows.map((row) => row.kind)).toEqual(['group'])
  })

  it('opens split rows for everyone with the setting, and per row by hand', () => {
    const split = {
      id: 's1' as Uuid,
      position: 0,
      category_id: null,
      amount: moneyFromCents(-500),
      memo: null,
      tag_ids: [],
    }
    const withSplits = txn({ id: 't1' as Uuid, splits: [split] })

    const closed = buildRows([withSplits], { showSplits: false })
    expect(closed.some((row) => row.kind === 'split')).toBe(false)

    const open = buildRows([withSplits], { showSplits: true })
    expect(open.some((row) => row.kind === 'split')).toBe(true)

    const expanded = buildRows([withSplits], {
      showSplits: false,
      expanded: new Set(['t1' as Uuid]),
    })
    expect(expanded.some((row) => row.kind === 'split')).toBe(true)
  })

  it('names each month in the chosen locale', () => {
    setDisplayLocale('de-DE')
    try {
      const rows = buildRows([txn({ id: 't2' as Uuid, date: '2026-07-02' })], { showSplits: false })
      expect(rows[0]).toMatchObject({ label: 'Juli 2026' })
    } finally {
      setDisplayLocale(null)
    }
  })
})

/**
 * Every row's height arrives in pixels from its density's token; these are
 * the tokens at the design scale, which `styles/rowHeights.test.ts` holds
 * them to.
 */
describe('the row height on a phone', () => {
  const rows = buildRows([txn({ id: 't1' as Uuid, splits: [] })], { showSplits: false })
  const group = rows[0]
  const transaction = rows[1]
  const px = { sm: 28, md: 34, lg: 44 }

  it('places a transaction at the two-line estimate at every density', () => {
    for (const height of Object.values(px)) {
      expect(rowHeight(transaction, height, NARROW_ROW_TWO_LINE)).toBeGreaterThanOrEqual(
        NARROW_ROW_TWO_LINE,
      )
    }
    expect(rowHeight(transaction, px.sm, NARROW_ROW_TWO_LINE)).toBe(NARROW_ROW_TWO_LINE)
  })

  it('is two lines taller than the densest desktop row', () => {
    expect(NARROW_ROW_TWO_LINE).toBeGreaterThan(px.lg)
  })

  it('leaves the group headings at the density, holding nothing to tap', () => {
    for (const height of Object.values(px)) {
      expect(rowHeight(group, height, NARROW_ROW_TWO_LINE)).toBe(height)
    }
  })

  it('gives a split line its density, like the row above it', () => {
    const part = {
      id: 's1' as Uuid,
      position: 0,
      category_id: null,
      amount: moneyFromCents(-500),
      memo: null,
      tag_ids: [],
    }
    const split = buildRows([txn({ id: 't2' as Uuid, splits: [part] })], {
      showSplits: true,
    }).find((row) => row.kind === 'split')
    expect(split).toBeDefined()
    if (split === undefined) return
    for (const height of Object.values(px)) expect(rowHeight(split, height)).toBe(height)
  })

  it('is not applied to a wide screen, which asks for no floor', () => {
    expect(rowHeight(transaction, px.sm)).toBe(px.sm)
  })
})
