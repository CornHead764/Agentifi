/** The dialog's line carries the amount; the text sent to the model must not. */

import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'

import {
  askTitle,
  kindNoun,
  questionText,
  subjectLine,
  suggestedQuestions,
  type AskKind,
  type AskSubject,
} from './rowQuestion'

const TODAY = new Date(2026, 8, 16)

const CHARGE: AskSubject = {
  kind: 'transaction',
  id: '00000000-0000-0000-0000-00000000f001',
  name: 'Barnaby Provisions',
  amount: moneyFromCents(-8400),
  date: '2026-09-12',
  detail: ['Everyday Checking', 'Groceries'],
  reads: ['/transactions/00000000-0000-0000-0000-00000000f001'],
}

describe('subjectLine', () => {
  it('reads as somebody would say the row', () => {
    expect(subjectLine(CHARGE, TODAY)).toBe(
      'Barnaby Provisions, -$84.00, Sep 12, Everyday Checking, Groceries',
    )
  })

  it('keeps the year on a row from another year', () => {
    expect(subjectLine({ ...CHARGE, date: '2024-09-12', detail: [] }, TODAY)).toBe(
      'Barnaby Provisions, -$84.00, Sep 12, 2024',
    )
  })

  it('drops what the row does not have rather than leaving gaps', () => {
    expect(subjectLine({ kind: 'tag', name: 'Vacation' }, TODAY)).toBe('Vacation')
  })

  it('shows a zero amount, which is a fact, not a missing one', () => {
    const line = subjectLine(
      { kind: 'bucket', name: 'Dining out', amount: moneyFromCents(0) },
      TODAY,
    )
    expect(line).toBe('Dining out, $0.00')
  })

  it('ignores blank detail entries', () => {
    expect(subjectLine({ ...CHARGE, detail: [null, '  ', undefined], date: null }, TODAY)).toBe(
      'Barnaby Provisions, -$84.00',
    )
  })
})

describe('questionText', () => {
  const sent = questionText(CHARGE, '  Why is this here?  ')

  it('names the row and its id', () => {
    expect(sent).toContain('one transaction in my ledger: Barnaby Provisions')
    expect(sent).toContain('Its id is 00000000-0000-0000-0000-00000000f001')
  })

  it('hands over the path to read rather than the figures', () => {
    expect(sent).toContain('read_endpoint on /transactions/00000000-0000-0000-0000-00000000f001')
    // The whole point: no amount, no date, nothing the model could answer from.
    expect(sent).not.toContain('84.00')
    expect(sent).not.toContain('2026-09-12')
  })

  it('carries the question, trimmed', () => {
    expect(sent.endsWith('My question: Why is this here?')).toBe(true)
  })

  it('joins several paths as a sentence', () => {
    const many = questionText(
      { ...CHARGE, reads: ['/transactions/f001', '/accounts/a1', '/payees/p1'] },
      'What is this?',
    )
    expect(many).toContain('read_endpoint on /transactions/f001, /accounts/a1 and /payees/p1')
  })

  it('still tells the model to look it up when no path is known', () => {
    const bare = questionText({ kind: 'goal', name: 'New roof' }, 'How far along?')
    expect(bare).toContain('Find it with the tools before answering')
    expect(bare).not.toContain('read_endpoint on ')
  })
})

const KINDS: AskKind[] = [
  'transaction',
  'holding',
  'account',
  'series',
  'rule',
  'guidance',
  'goal',
  'watchlist',
  'bucket',
  'bill',
  'order',
  'asset',
  'category',
  'tag',
  'networth',
  'portfolio',
]

describe('suggestedQuestions', () => {
  it('offers something for every kind of row a menu can raise', () => {
    for (const kind of KINDS) {
      const chips = suggestedQuestions(kind)
      expect(chips.length).toBeGreaterThan(0)
      for (const chip of chips) expect(chip.trim()).not.toBe('')
    }
  })

  it('asks what the ledger shows, never what to do about it', () => {
    for (const kind of KINDS) {
      for (const chip of suggestedQuestions(kind)) {
        expect(chip.toLowerCase()).not.toMatch(/should i|recommend|worth buying|good idea/)
      }
    }
  })

  it('names the row type in the heading', () => {
    expect(askTitle('holding')).toBe('Ask about this investment holding')
    expect(kindNoun('bucket')).toBe('spending plan bucket')
  })
})
