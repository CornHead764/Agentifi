/**
 * The register's cells, rendered one at a time: the virtualized grid draws no
 * rows without a scroll element. The narrow register's `TwoLineRow` is
 * rendered the same way at the end.
 */

import type { ReactNode } from 'react'
import { describe, expect, it } from 'vitest'

import { setDisplayLocale } from '@/lib/locale'
import { COLUMNS } from '@/lib/transactions/columns'
import { moneyFromCents } from '@/lib/money'
import type { Transaction, Uuid } from '@/lib/transactions/types'
import { transaction } from '@/test/builders'
import { renderScreen } from '@/test/renderScreen'
import { RegisterCell, SplitParts, TwoLineRow } from './RegisterCells'
import { RegisterContext, type RegisterView } from './register-context'

const noop = () => undefined
const VIEW: RegisterView = {
  lookups: {
    accounts: [],
    categories: [],
    tags: [],
    accountName: () => 'Cashback Mastercard',
    categoryName: (id) => (id === null ? 'Uncategorized' : 'Fast Food'),
    tagName: () => 'tag',
    frequentCategoryIds: [],
  },
  actions: {
    edit: noop,
    setReviewed: noop,
    setTags: noop,
    openDetail: noop,
    applySuggestion: noop,
    discardSuggestion: noop,
    decidingSuggestion: false,
    refuse: noop,
    showRun: noop,
  },
  selection: {
    enabled: false,
    active: false,
    ids: new Set(),
    toggle: noop,
    begin: noop,
    visibleIds: [],
    toggleAll: noop,
  },
  sections: { collapsed: new Set(), toggle: noop },
  swipe: { left: 'menu', right: 'review' },
  multiAccount: true,
}

function cell(columnId: string, row: Transaction): string {
  const column = COLUMNS.find((one) => one.id === columnId)
  if (!column) throw new Error(`no column ${columnId}`)
  return renderScreen(
    <RegisterContext.Provider value={VIEW}>
      <RegisterCell column={column} txn={row} />
    </RegisterContext.Provider>,
  )
}

describe('the register cells', () => {
  it('keeps the statement name locked while the payee stays editable', () => {
    expect(cell('statement_name', transaction())).toContain('SQ *COFFEE')
    expect(cell('statement_name', transaction())).not.toContain('<input')
    expect(cell('payee', transaction())).toContain('Harbor Coffee')
  })

  it('exposes the reviewed toggle with its true state', () => {
    expect(cell('reviewed', transaction())).toContain('aria-pressed="false"')
    expect(cell('reviewed', transaction({ is_reviewed: true }))).toContain('aria-pressed="true"')
  })

  it('draws an exclusion glyph only for the flag that is set', () => {
    const excluded = cell('exclusion', transaction({ excluded_from_reports: true }))
    expect(excluded.toLowerCase()).toContain('reports')
    const clean = cell('exclusion', transaction())
    expect(clean.toLowerCase()).not.toContain('reports')
  })

  it('counts attachments on the paperclip only past one file', () => {
    expect(cell('attachment', transaction({ attachment_count: 1 }))).not.toContain('txn-mark__count')
    const three = cell('attachment', transaction({ attachment_count: 3 }))
    expect(three).toContain('txn-mark__count')
    expect(three).toContain('>3<')
  })

  it('marks a row owing a receipt in the attachment column, and offers to open it', () => {
    const owed = cell('attachment', transaction({ receipt_status: 'missing' }))
    expect(owed).toContain('txn-mark--owed')
    expect(owed).toContain('Needs a receipt: add one')
    expect(owed).toContain('<button')

    for (const status of ['on_file', 'not_needed', null] as const) {
      expect(cell('attachment', transaction({ receipt_status: status }))).not.toContain(
        'txn-mark--owed',
      )
    }
  })

  it('renders the amount signed', () => {
    expect(cell('amount', transaction())).toContain('18.50')
  })

  it('writes the date in the chosen locale', () => {
    setDisplayLocale('de-DE')
    try {
      expect(cell('date', transaction())).toContain('21. Aug. 2026')
    } finally {
      setDisplayLocale(null)
    }
  })
})

const SUGGESTION = {
  action_id: 'p1' as Uuid,
  conversation_id: 'c1' as Uuid,
  run_id: 'r1' as Uuid,
  tool: 'update_transaction',
  summary: 'Costco is Groceries, as the last 11 times',
  category_id: 'c1' as Uuid,
  splits: [],
  created_at: '2026-09-04T12:00:08Z',
}

describe('the review icon in review mode', () => {
  function reviewCell(row: Transaction, reviewMode: boolean): string {
    const column = COLUMNS.find((one) => one.id === 'reviewed')!
    return renderScreen(
      <RegisterContext.Provider value={{ ...VIEW, actions: { ...VIEW.actions, reviewMode } }}>
        <RegisterCell column={column} txn={row} />
      </RegisterContext.Provider>,
    )
  }

  it('says it marks the row reviewed', () => {
    expect(reviewCell(transaction({ is_reviewed: false }), true)).toContain('Mark as reviewed')
    expect(reviewCell(transaction({ is_reviewed: false }), false)).toContain(
      'Review this transaction',
    )
  })

  it('still says it opens a row with a suggestion', () => {
    expect(reviewCell(transaction({ suggestion: SUGGESTION }), true)).toContain(
      'Review this transaction',
    )
  })
})

describe('a row with a suggestion waiting on it', () => {
  it('shows the suggested category rather than what the row says', () => {
    // The proposal must be on screen, marked as one.
    const html = cell('category', transaction({ suggestion: SUGGESTION }))

    expect(html).toContain('Fast Food')
    expect(html).toContain('cell-suggested')
    expect(html).toContain('Suggested category: Fast Food')
    expect(html).not.toContain('Uncategorized')
  })

  it('offers a tick that approves it in place, saying what approving does', () => {
    const html = cell('category', transaction({ suggestion: SUGGESTION }))

    expect(html).toContain('Approve: file this under Fast Food')
    // And a picker on the same cell, so choosing something else is one click
    // rather than a discard and a retype.
    expect(html).toContain('Choose a different one')
  })

  it('sends a proposed split to the dialog instead of naming one category', () => {
    const html = cell(
      'category',
      transaction({ suggestion: { ...SUGGESTION, tool: 'split_transaction', category_id: null } }),
    )

    expect(html).toContain('Suggested split')
    expect(html).not.toContain('Approve: file this under')
  })

  it('leaves an ordinary row category cell exactly as it was', () => {
    const html = cell('category', transaction({ category_id: 'c1' as Uuid }))

    expect(html).toContain('Category: Fast Food')
    expect(html).not.toContain('cell-suggested')
  })

  it('turns the reviewed tick into a way into the review, not a silent tick', () => {
    // Ticking an unreviewed row without showing what was proposed for it is
    // how a suggestion would get thrown away by somebody tidying up.
    expect(cell('reviewed', transaction())).toContain('Review this transaction')
    expect(cell('reviewed', transaction({ is_reviewed: true }))).toContain('Mark as unreviewed')
    expect(cell('reviewed', transaction({ suggestion: SUGGESTION }))).toContain('data-waiting="true"')
  })
})

function twoLine(row: Transaction): string {
  return renderScreen(
    <RegisterContext.Provider value={VIEW}>
      <TwoLineRow txn={row} />
    </RegisterContext.Provider>,
  )
}

describe('the register row on a phone', () => {
  it('carries the payee, the category and the date whole', () => {
    const html = twoLine(transaction({ payee: 'Pacific Gas & Electric', category_id: 'c1' as Uuid }))

    expect(html).toContain('Pacific Gas &amp; Electric')
    expect(html).toContain('Fast Food')
    expect(html).toContain('Aug 21, 2026')
  })

  it('marks a row owing a receipt beside the category, without a second button in the line', () => {
    const owed = twoLine(transaction({ receipt_status: 'missing' }))
    expect(owed).toContain('txn-mark--owed')
    expect(owed).toContain('aria-label="Needs a receipt"')
    expect(owed.split('<button').length - 1).toBe(1)

    expect(twoLine(transaction({ receipt_status: 'not_needed' }))).not.toContain('txn-mark--owed')
  })

  it('falls back to the statement name where there is no payee', () => {
    expect(twoLine(transaction({ payee: '' }))).toContain('SQ *COFFEE')
  })

  it('draws a suggestion as the highlighted category itself, not a badge beside it', () => {
    // The meta line is one line of small print; a badge saying "Suggested"
    // would cost it the category name it is suggesting.
    const html = twoLine(transaction({ suggestion: SUGGESTION }))

    expect(html).toContain('class="badge badge--warning txn-line__category"')
    expect(html).toContain('Fast Food')
    expect(html).not.toContain('>Suggested<')
  })

    // The date sits in the amount's track, so a list of rows reads as a column
    // of dates.
  it('puts the date in its own slot rather than after the category', () => {
    const html = twoLine(transaction({ category_id: 'c1' as Uuid }))

    expect(html).toContain('txn-line__when')
    expect(html).not.toContain('Fast Food · Aug 21, 2026')
    expect(html).not.toContain('Fast Food &middot; Aug 21, 2026')
  })

  // The register lifts pending rows into a section headed "Pending", so a
  // badge on each row would say it twice and take the room the category needs.
  it('does not badge a pending row: the section it sits in already says so', () => {
    const html = twoLine(transaction({ is_pending: true, category_id: 'c1' as Uuid }))

    expect(html).toContain('Fast Food')
    expect(html).not.toContain('Pending')
  })

    // The payee is the only arbitrarily long field, so it alone may clip.
  it('clips the payee and nothing else', () => {
    const html = twoLine(transaction())

    expect(html).toContain('txn-line__payee')
    expect(html).toContain('txn-line__meta')
  })

  // Editing on a phone happens in the detail dialog: a 40px column is not a
  // text field, and the row is one button onto it.
  it('opens no inline editor', () => {
    const html = twoLine(transaction())

    expect(html).not.toContain('<input')
    expect(html).not.toContain('cell-edit')
  })

    // Only when true, so an ordinary row shows a bare name.
  it('says nothing after the payee on an ordinary row', () => {
    const html = twoLine(transaction({ excluded_from_reports: false }))

    expect(html).not.toContain('txn-line__glyphs')
  })

  it('marks a recurring row, a transfer leg, a split, an exclusion and a flag', () => {
    const html = twoLine(
      transaction({
        series_id: 's1' as Uuid,
        transfer_pair_id: 't2' as Uuid,
        splits: [{ id: 'x' }, { id: 'y' }] as never,
        excluded_from_reports: true,
        user_flag: 'flagged',
      }),
    )

    expect(html).toContain('Recurring item')
    expect(html).toContain('One leg of a transfer')
    expect(html).toContain('Split 2 ways')
    expect(html).toContain('Excluded from reports')
    expect(html).toContain('Flagged')
  })

  it('names a flag by its note, which is what the flag was for', () => {
    const html = twoLine(transaction({ user_flag: 'flagged', user_flag_note: 'Check this refund' }))

    expect(html).toContain('Check this refund')
  })

  it('marks a purchase paid by payroll deduction', () => {
    const html = twoLine(transaction({ padding_txn_id: 'pad-1' as Uuid }))

    expect(html).toContain('Paid by payroll deduction')
  })
})

describe('the payee cell', () => {
  it('carries a mark after a purchase paid by payroll deduction, and none otherwise', () => {
    expect(cell('payee', transaction({ padding_txn_id: 'pad-1' as Uuid }))).toContain(
      'Paid by payroll deduction',
    )
    expect(cell('payee', transaction())).not.toContain('cell-marked')
  })
})

const NAMED: RegisterView = {
  ...VIEW,
  lookups: {
    ...VIEW.lookups,
    categoryName: (id) =>
      ({ groceries: 'Groceries', household: 'Household' })[id ?? ''] ?? 'Uncategorized',
  },
}

/** A $100 receipt split $80 groceries / $20 household, under a groceries filter. */
function partialRow(overrides: Partial<Transaction> = {}): Transaction {
  const split = (id: string, cents: number, category: string) => ({
    id: id as Uuid,
    position: 0,
    amount: moneyFromCents(cents),
    category_id: category as Uuid,
    memo: null,
    tag_ids: [],
  })
  return transaction({
    amount: moneyFromCents(-10_000),
    splits: [split('s1', -8_000, 'groceries'), split('s2', -2_000, 'household')],
    matched_split_ids: ['s1' as Uuid],
    matched_amount: moneyFromCents(-8_000),
    ...overrides,
  })
}

function named(node: ReactNode): string {
  return renderScreen(<RegisterContext.Provider value={NAMED}>{node}</RegisterContext.Provider>)
}

function namedCell(columnId: string, row: Transaction): string {
  const column = COLUMNS.find((one) => one.id === columnId)
  if (!column) throw new Error(`no column ${columnId}`)
  return named(<RegisterCell column={column} txn={row} />)
}

describe('a split row a filter kept only part of', () => {
  it('shows the matching split and its amount, over the whole transaction', () => {
    const amount = namedCell('amount', partialRow())
    expect(amount).toContain('partial-amount')
    expect(amount).toContain('80.00')
    expect(amount).toContain('100.00')
    // Not colour alone: the glyph and the words say it to a screen reader too.
    expect(amount).toContain('Matching part of a split')
  })

  it('names only the matching category, and still opens the splits', () => {
    const category = namedCell('category', partialRow())
    expect(category).toContain('Groceries')
    expect(category).not.toContain('Household')
    expect(category).not.toContain('2 categories')
    expect(category).toContain('part of a 2-way split')
  })

  it('does the same on a phone', () => {
    const html = named(<TwoLineRow txn={partialRow()} />)
    expect(html).toContain('txn-line__category--partial')
    expect(html).toContain('Groceries')
    expect(html).not.toContain('Household')
    expect(html).toContain('80.00')
    expect(html).toContain('100.00')
  })

  it('leaves a split row the filter kept whole as it always was', () => {
    const whole = partialRow({ matched_split_ids: null, matched_amount: null })
    expect(namedCell('category', whole)).toContain('2 categories')
    expect(namedCell('amount', whole)).not.toContain('partial-amount')
    expect(namedCell('amount', whole)).toContain('100.00')
  })
})

describe('the breakdown of a split row', () => {
  it('shows "N categories" with no parts inline, and leaves a plain row alone', () => {
    const whole = partialRow({ matched_split_ids: null, matched_amount: null })
    const html = namedCell('category', whole)
    expect(html).toContain('2 categories')
    expect(html).not.toContain('Groceries')
    expect(html).not.toContain('Household')
    expect(html).not.toContain('80.00')

    const plain = namedCell('category', transaction({ category_id: 'groceries' as Uuid }))
    expect(plain).toContain('Groceries')
    expect(plain).not.toContain('split-tip')
    expect(plain).not.toContain('categories')
  })

  it('lists each part with its category, memo and amount', () => {
    const row = partialRow({ matched_split_ids: null, matched_amount: null })
    row.splits[1] = { ...row.splits[1], memo: 'Dish soap' }
    const html = named(<SplitParts txn={row} />)
    expect(html).toContain('Groceries')
    expect(html).toContain('Household')
    expect(html).toContain('80.00')
    expect(html).toContain('20.00')
    expect(html).toContain('Dish soap')
  })
})
