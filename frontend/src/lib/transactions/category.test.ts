import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'
import { transaction } from '@/test/builders'

import { categoryIds, needsCategory } from './category'
import type { Split } from './types'

function split(id: string, categoryId: string | null): Split {
  return { id, position: 0, amount: moneyFromCents(-2000), category_id: categoryId, memo: null, tag_ids: [] }
}

describe('needsCategory', () => {
  it('is an unsplit row with no category', () => {
    expect(needsCategory(transaction())).toBe(true)
    expect(needsCategory(transaction({ category_id: 'gifts' }))).toBe(false)
  })

  it('is not a split row whose every part is filed, though its parent has no category', () => {
    const filed = transaction({ splits: [split('s1', 'gifts'), split('s2', 'household')] })
    expect(filed.category_id).toBeNull()
    expect(needsCategory(filed)).toBe(false)
  })

  it('is a split row with a part still unfiled', () => {
    expect(needsCategory(transaction({ splits: [split('s1', 'gifts'), split('s2', null)] }))).toBe(true)
  })

  it('is never a paired transfer leg', () => {
    expect(needsCategory(transaction({ transfer_pair_id: 'pair' }))).toBe(false)
  })
})

describe('categoryIds', () => {
  it('is the row’s own category, or none', () => {
    expect(categoryIds(transaction())).toEqual([])
    expect(categoryIds(transaction({ category_id: 'gifts' }))).toEqual(['gifts'])
  })

  it('is a split row’s splits’ categories, never its parent’s', () => {
    const row = transaction({ splits: [split('s1', 'gifts'), split('s2', null), split('s3', 'household')] })
    expect(categoryIds(row)).toEqual(['gifts', 'household'])
  })
})
