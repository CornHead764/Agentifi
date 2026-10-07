/**
 * A goal needs an account to live in. With none in the space, the editor adds
 * one on the spot rather than telling the reader to go and add it first.
 */

import { describe, expect, it, vi } from 'vitest'

vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).dialogStub,
}))

import type { Goal } from '@/lib/goals'
import { parseMoney } from '@/lib/money'
import { renderScreen } from '@/test/renderScreen'

import { GoalEditor } from './GoalEditor'

const GOAL = {
  id: 'g1',
  name: 'Rainy day',
  emoji: null,
  account_id: '',
  funding_account_ids: [],
  target_amount: parseMoney('1000.00'),
  target_on: null,
  is_taken_from_plan: false,
} as unknown as Goal

function render(accounts: { id: string; name: string }[]): string {
  return renderScreen(
    <GoalEditor open onOpenChange={() => {}} goal={GOAL} accounts={accounts} />,
  )
}

describe('GoalEditor account field', () => {
  it('offers to add the account in place when the space has none', () => {
    const html = render([])
    expect(html).toContain('New account')
    expect(html).not.toContain('Add one first')
  })

  it('is the ordinary picker once there is an account to choose', () => {
    const html = render([{ id: 'a1', name: 'Everyday Savings' }])
    expect(html).toContain('Select account')
  })
})
