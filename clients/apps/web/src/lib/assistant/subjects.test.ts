/** `reads` is the only part of a subject the model sees, so these check the paths. */

import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'

import { questionText, subjectLine } from './rowQuestion'
import {
  accountSubject,
  billSubject,
  bucketSubject,
  categorySubject,
  goalSubject,
  guidanceSubject,
  holdingSubject,
  netWorthSubject,
  orderSubject,
  portfolioSubject,
  ruleSubject,
  seriesSubject,
  tagSubject,
  transactionSubject,
  watchlistSubject,
} from './subjects'

const TXN = {
  id: 'f1',
  account_id: 'a1',
  payee: 'Barnaby Provisions',
  statement_name: 'BARNABY PROV #4471',
  amount: moneyFromCents(-8400),
  date: '2026-09-12',
  effective_date: null,
  splits: [],
}

describe('transactionSubject', () => {
  it('points at the row and the account it is in', () => {
    expect(transactionSubject(TXN).reads).toEqual(['/transactions/f1', '/accounts/a1'])
  })

  it('falls back to the bank wording when the payee is empty', () => {
    expect(transactionSubject({ ...TXN, payee: '   ' }).name).toBe('BARNABY PROV #4471')
  })

  it('reports as the reporting date, not the posting one', () => {
    // Trap 4: `effective_date` is the reporting date wherever both exist.
    const moved = transactionSubject({ ...TXN, effective_date: '2026-10-01' })
    expect(moved.date).toBe('2026-10-01')
  })

  it('says a row is split, since that changes what the question means', () => {
    const split = transactionSubject({ ...TXN, splits: [{}, {}, {}] })
    expect(subjectLine(split, new Date(2026, 8, 16))).toContain('3 splits')
  })

  it('carries the names the screen already had', () => {
    const named = transactionSubject(TXN, { account: 'Everyday Checking', category: 'Groceries' })
    expect(subjectLine(named, new Date(2026, 8, 16))).toBe(
      'Barnaby Provisions, -$84.00, Sep 12, Everyday Checking, Groceries',
    )
  })
})

describe('the rest of the builders', () => {
  it('reads an account through its record and its summary', () => {
    expect(accountSubject({ id: 'a1', name: 'Everyday Checking' }).reads).toEqual([
      '/accounts/a1',
      '/accounts/a1/summary',
    ])
  })

  it('points a holding at the portfolio and the security behind it', () => {
    expect(holdingSubject({ security_id: 's1', symbol: 'ZZZX' }).reads).toEqual([
      '/holdings',
      '/securities/s1',
    ])
  })

  it('asks for a holding with no security by the portfolio alone', () => {
    expect(holdingSubject({ symbol: 'ZZZX' }).reads).toEqual(['/holdings'])
  })

  it('reads a series with its history, since the question is usually about change', () => {
    expect(seriesSubject({ id: 'r1', label: 'Water' }).reads).toEqual([
      '/series/r1',
      '/series/r1/history',
    ])
  })

  it('prefers a series label over the wording it matches on', () => {
    // Trap 7: `description` is matching input, not a label.
    const series = seriesSubject({ id: 'r1', label: 'Water', description: 'CITY UTIL ACH' })
    expect(series.name).toBe('Water')
  })

  it('offers a rule with what it would catch', () => {
    expect(ruleSubject({ id: 'u1', name: 'File the gym' }).reads).toEqual([
      '/rules/u1',
      '/rules/u1/preview',
    ])
  })

  it('keeps guidance on its own resource, not the rules one', () => {
    expect(guidanceSubject({ id: 'g1', name: 'Groceries note' }).reads).toEqual(['/guidance/g1'])
  })

  it('names the month a bucket belongs to, because the plan is per month', () => {
    const bucket = bucketSubject({ name: 'Planned spending', month: '2026-09-01' })
    expect(bucket.reads).toEqual(['/spending-plan/2026-09-01'])
    expect(subjectLine(bucket, new Date(2026, 8, 16))).toBe('Planned spending, 2026-09')
  })

  it('sends a bill to its series when it has one', () => {
    expect(billSubject({ name: 'Water', due_on: '2026-09-20', series_id: 'r1' }).reads).toEqual([
      '/occurrences',
      '/series/r1',
    ])
  })

  it('sends an order to the merchant that sold it, never the other one', () => {
    // Ground rule 6: the merchant is a value, never an assumption.
    expect(orderSubject({ id: 'o1', merchant: 'costco', title: 'Costco order 77' }).reads).toEqual([
      '/merchants/costco/orders',
    ])
  })

  it('reads goals as a list, which is the only route there is', () => {
    expect(goalSubject({ id: 'g1', name: 'New roof' }).reads).toEqual(['/goals'])
  })

  it('reads a watchlist, a category and a tag by id', () => {
    expect(watchlistSubject({ id: 'w1', name: 'Eating out' }).reads).toEqual(['/watchlists/w1'])
    expect(categorySubject({ id: 'c1', name: 'Groceries' }).reads).toEqual(['/categories/c1'])
    expect(tagSubject({ id: 't1', name: 'Vacation' }).reads).toEqual(['/tags/t1'])
  })

  it('reads the whole net worth and the whole portfolio from the routes their screens use', () => {
    expect(netWorthSubject().reads).toEqual(['/net-worth', '/accounts'])
    expect(portfolioSubject().reads).toEqual(['/holdings'])
  })
})

describe('what actually leaves the browser', () => {
  it('never carries a figure, whichever builder made the subject', () => {
    const subjects = [
      transactionSubject(TXN, { account: 'Everyday Checking' }),
      accountSubject({ id: 'a1', name: 'Everyday Checking', balance: moneyFromCents(412_00) }),
      holdingSubject({ security_id: 's1', symbol: 'ZZZX', market_value: moneyFromCents(900_00) }),
      bucketSubject({ name: 'Dining out', month: '2026-09-01', amount: moneyFromCents(140_00) }),
    ]
    for (const subject of subjects) {
      const sent = questionText(subject, 'What is going on here?')
      expect(sent).not.toMatch(/\d+\.\d\d/)
    }
  })
})
