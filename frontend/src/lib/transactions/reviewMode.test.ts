import { describe, expect, it } from 'vitest'

import { transaction } from '@/test/builders'
import { reviewIconAction } from './reviewMode'
import type { Suggestion } from './types'

const SUGGESTION = {
  action_id: 'a1',
  tool: 'update_transaction',
  category_id: 'c1',
  summary: '',
  splits: [],
  created_at: '2026-09-04T12:00:08Z',
} as unknown as Suggestion

describe('the review icon', () => {
  it('opens the dialog out of review mode', () => {
    expect(reviewIconAction(transaction({ is_reviewed: false }), false)).toBe('open')
  })

  it('marks reviewed in review mode, without opening the dialog', () => {
    expect(reviewIconAction(transaction({ is_reviewed: false }), true)).toBe('review')
  })

  it('un-reviews a reviewed row in either mode', () => {
    expect(reviewIconAction(transaction({ is_reviewed: true }), true)).toBe('unreview')
    expect(reviewIconAction(transaction({ is_reviewed: true }), false)).toBe('unreview')
  })

  it('opens a row with a suggestion waiting even in review mode', () => {
    expect(
      reviewIconAction(transaction({ is_reviewed: false, suggestion: SUGGESTION }), true),
    ).toBe('open')
  })
})
