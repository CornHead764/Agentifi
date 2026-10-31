import { describe, expect, it } from 'vitest'

import type { CashFlowLine } from '@/lib/clients/upcoming'

import { accountColors } from './accountColors'

const line = (account_id: string) => ({ account_id }) as CashFlowLine

describe('accountColors', () => {
  it('colours each account by its place in the whole projection, not in a group or a selection', () => {
    // Checking and savings are one group, the card another; the card is the
    // third line whether or not either of the others is ticked.
    const colorOf = accountColors([line('checking'), line('savings'), line('card')])
    expect(colorOf('checking')).toBe('var(--series-1)')
    expect(colorOf('savings')).toBe('var(--series-2)')
    expect(colorOf('card')).toBe('var(--series-3)')
  })
})
