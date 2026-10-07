/**
 * What the backfill confirm says, taken apart from the dialog around it: the
 * dialog is a portal, which a static render does not draw.
 */

import { describe, expect, it } from 'vitest'

import { describeBackfill } from '@/lib/clients/merchant'

import { backfillWords } from './actions'

describe('the confirm before a backfill', () => {
  it("says an Amazon backfill opens each order's page, paced, and stops at a sign-in", () => {
    const words = backfillWords('amazon', 'Alex', 7, false)
    expect(words.title).toBe("Backfill Alex's invoices?")
    expect(words.how).toContain("Opens each order's invoice page at Amazon, newest first")
    expect(words.how).toContain('If Amazon asks to sign in it stops there')
    expect(words.body).toBe('7 orders on file have no invoice yet.')
    expect(words.submit).toBe('Backfill 7 invoices')
  })

  it('says a Costco backfill asks Costco nothing', () => {
    const words = backfillWords('costco', 'Alex', 1, false)
    expect(words.how).toBe(
      'Lays out a receipt for each purchase on file from the lines already read, and asks Costco nothing.',
    )
    expect(words.body).toBe('1 purchase on file has no invoice yet.')
    expect(words.submit).toBe('Backfill 1 invoice')
  })

  it('counts first, says when it could not, and offers nothing to start with none left', () => {
    expect(backfillWords('amazon', 'Alex', undefined, false).body).toBe('Counting…')
    expect(backfillWords('amazon', 'Alex', undefined, true).body).toBe(
      'The orders without an invoice could not be counted.',
    )
    const none = backfillWords('amazon', 'Alex', 0, false)
    expect(none.body).toBe(
      'Every order on file has its invoice, or has come back without one three times.',
    )
    expect(none.submit).toBe('Backfill')
    expect(backfillWords('costco', 'Alex', 0, false).body).toBe(
      'Every purchase on file that a receipt can be laid out for already has one.',
    )
  })
})

describe('what the row says about a backfill', () => {
  const ended = {
    running: false,
    total: 3,
    done: 3,
    filed: 1,
    left: 0,
    stopped: '',
    finished_at: null,
  }

  it('says nothing before one has run', () => {
    expect(describeBackfill('amazon', null)).toBeNull()
  })

  it('counts a running one through, or says only that it runs before it has a total', () => {
    expect(describeBackfill('amazon', { ...ended, running: true, done: 1 })).toBe(
      'Backfilling invoices: 1 of 3…',
    )
    expect(
      describeBackfill('amazon', {
        ...ended,
        running: true,
        total: 0,
        done: 0,
      }),
    ).toBe('Backfilling invoices…')
  })

  it("says what it filed and left, in the merchant's own words", () => {
    expect(describeBackfill('amazon', ended)).toBe(
      'Last invoice backfill: 1 invoice filed, no order left without one.',
    )
    expect(describeBackfill('costco', { ...ended, filed: 0, left: 2 })).toBe(
      'Last invoice backfill: 0 invoices filed, 2 purchases still without one.',
    )
  })

  it('says why it stopped early, once', () => {
    expect(
      describeBackfill('amazon', {
        ...ended,
        left: 1,
        stopped: 'Amazon showed a check page.',
      }),
    ).toBe(
      'Last invoice backfill: 1 invoice filed, 1 order still without one. It stopped early: Amazon showed a check page.',
    )
  })
})
