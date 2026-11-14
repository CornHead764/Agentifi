/** A day's Out figure, which calculations.md §15 lets go below zero. */

import { describe, expect, it } from 'vitest'

import type { ReviewDay } from '@/lib/clients/review'
import { moneyFromCents } from '@agentifi/core/money'
import { renderScreen } from '@/test/renderScreen'

import { DaySection, type RowHandlers } from './DaySection'

const HANDLERS: RowHandlers = {
  selectedId: null,
  approvedIds: new Set(),
  pickingId: null,
  categories: [],
  frequentCategoryIds: [],
  onSelect: () => {},
  onOpen: () => {},
  onApprove: () => {},
  onPick: () => {},
  onFile: () => {},
  onApproveDay: () => {},
}

function dayOut(cents: number): string {
  const day: ReviewDay = { date: '2026-06-14', out: moneyFromCents(cents), needs_a_look: 0, rows: [] }
  const markup = renderScreen(<DaySection day={day} handlers={HANDLERS} />)
  return /class="review-day__sub">([\s\S]*?)<\/p>/.exec(markup)?.[1].replace(/<[^>]+>/g, '') ?? ''
}

describe('a review day', () => {
  it('says what went out', () => {
    expect(dayOut(6000)).toBe('$60.00 out')
  })

  it('says what came back when refunds outweigh spending', () => {
    expect(dayOut(-2000)).toBe('$20.00 back')
  })
})
