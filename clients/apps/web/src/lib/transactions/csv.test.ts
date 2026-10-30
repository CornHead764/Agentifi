import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'
import { transaction } from '@/test/builders'

import { registerToCsv } from './csv'
import type { Split } from './types'

const names: Record<string, string> = { gifts: 'Gifts', household: 'Household' }
const categoryName = (id: string | null) => (id === null ? 'Uncategorized' : (names[id] ?? 'Unknown'))

function split(id: string, categoryId: string | null): Split {
  return { id, position: 0, amount: moneyFromCents(-1000), category_id: categoryId, memo: null, tag_ids: [] }
}

function dataLine(csv: string): string {
  return csv.split('\n')[1]
}

describe('registerToCsv', () => {
  it('names a split row by its splits’ categories, not its empty parent', () => {
    const row = transaction({
      splits: [split('s1', 'gifts'), split('s2', 'household'), split('s3', 'gifts')],
    })
    expect(dataLine(registerToCsv([row], () => 'Checking', categoryName))).toContain(',"Gifts, Household",')
  })

  it('names an unfiled split as uncategorized', () => {
    const row = transaction({ splits: [split('s1', 'gifts'), split('s2', null)] })
    expect(dataLine(registerToCsv([row], () => 'Checking', categoryName))).toContain(',"Gifts, Uncategorized",')
  })
})
