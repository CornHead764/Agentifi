import { describe, expect, it } from 'vitest'

import {
  ACTIVITY_COLUMNS,
  COLUMNS,
  customizableColumns,
  defaultPrefs,
  fitColumns,
  floorsRem,
  gridTemplate,
  hiddenReason,
  trackMinimumRem,
  visibleColumns,
  type ColumnDef,
  type ColumnId,
} from './columns'

/** Tracks are rem; these tests read them back at the design scale. */
const designPx = (track: string) => Number.parseFloat(track) * 16

describe('the date column', () => {
  it('is wide enough for a date with its year', () => {
    // Measured in the browser: "May 31, 2026" is 113px, and the cell spends 22.
    const date = COLUMNS.find((column) => column.id === 'date')
    expect(Number.parseFloat(date?.track ?? '0') * 20 - 22).toBeGreaterThanOrEqual(113)
  })

  it('cannot be switched off, being what the register is sorted by', () => {
    expect(COLUMNS.find((column) => column.id === 'date')?.locked).toBe(true)
  })
})

describe('the money columns', () => {
  it('fit a six-figure balance, which is 118px with the padding', () => {
    for (const id of ['amount', 'balance'] as const) {
      const track = COLUMNS.find((column) => column.id === id)?.track ?? '0'
      expect(designPx(track)).toBeGreaterThanOrEqual(118)
    }
  })
})

describe('the running balance column', () => {
  it('is drawn for one account', () => {
    const columns = visibleColumns(defaultPrefs(), undefined, 'one-account')
    expect(columns.map((column) => column.id)).toContain('balance')
  })

  it('is dropped across several, where every row of it would be an em dash', () => {
    const columns = visibleColumns(defaultPrefs(), undefined, 'many-accounts')
    expect(columns.map((column) => column.id)).not.toContain('balance')
  })

  it('leaves the stored preference alone, so it returns with the account', () => {
    const prefs = defaultPrefs()
    visibleColumns(prefs, undefined, 'many-accounts')
    expect(prefs.visible.balance).toBe(true)
  })
})

describe('the grid template', () => {
  it('carries one track per column plus the row menu', () => {
    const columns = visibleColumns(defaultPrefs(), undefined, 'one-account')
    expect(gridTemplate(columns).split(' ').length).toBeGreaterThan(columns.length)
  })
})

describe('the narrow column set', () => {
  /** Tracks, kept whole — `minmax(0, 1fr)` has a space in it and is one track. */
  const tracks = (template: string) => template.match(/minmax\([^)]*\)|\S+/g) ?? []

  const narrow = (prefs = defaultPrefs(), only?: readonly ColumnId[]) =>
    visibleColumns(prefs, only, 'one-account', true)

  it('is date, payee, category and amount, in the register order', () => {
    expect(narrow().map((column) => column.id)).toEqual(['date', 'payee', 'category', 'amount'])
  })

  it('shows them whatever the gear menu was set to, and shows nothing else', () => {
    const prefs = defaultPrefs()
    prefs.visible.amount = false
    prefs.visible.tags = true
    prefs.visible.check_number = true

    expect(narrow(prefs).map((column) => column.id)).toEqual([
      'date',
      'payee',
      'category',
      'amount',
    ])
  })

  it('leaves the stored preferences alone, so the chosen set returns with the width', () => {
    const prefs = defaultPrefs()
    narrow(prefs)
    expect(prefs.visible.tags).toBe(true)
    expect(visibleColumns(prefs, undefined, 'one-account').map((column) => column.id)).toContain(
      'tags',
    )
  })

  it('keeps the locked columns, which is three of the four', () => {
    const locked = COLUMNS.filter((column) => column.locked).map((column) => column.id)
    const shown = narrow().map((column) => column.id)
    for (const id of locked) expect(shown).toContain(id)
  })

  it('drops the running balance across several accounts, as the wide set does', () => {
    const columns = visibleColumns(defaultPrefs(), undefined, 'many-accounts', true)
    expect(columns.map((column) => column.id)).not.toContain('balance')
  })

  it('narrows to the activity columns on the Spending and Income tabs', () => {
    expect(narrow(defaultPrefs(), ACTIVITY_COLUMNS).map((column) => column.id)).toEqual([
      'date',
      'payee',
      'category',
      'amount',
    ])
  })

  it('lays out four tracks and the row menu', () => {
    const template = tracks(gridTemplate(narrow()))
    expect(template).toHaveLength(5)
    expect(template.at(-1)).toBe('2rem')
  })

  // base.css puts the root at 100% below 48rem, so a rem is 16px on a phone.
  const phonePx = (track: string) => Number.parseFloat(track) * 16

  it('leaves the text columns most of a 390px phone', () => {
    // 361px of card less the fixed tracks must leave payee and category 160px.
    const fixed = tracks(gridTemplate(narrow()))
      .filter((track) => track.endsWith('rem'))
      .reduce((total, track) => total + phonePx(track), 0)
    expect(fixed).toBeLessThanOrEqual(361 - 160)
  })

  it('fits "Sep 30" — the cell drops the year here — with 0.5rem of padding', () => {
    const date = narrow().find((column) => column.id === 'date')
    expect(phonePx(date?.track ?? '0')).toBeGreaterThanOrEqual(6 * 7.4 + 8)
  })

  it('fits a signed six-figure amount on the same arithmetic', () => {
    const amount = narrow().find((column) => column.id === 'amount')
    expect(phonePx(amount?.track ?? '0')).toBeGreaterThanOrEqual(11 * 7.4 + 8)
  })
})

describe('fitting the columns to the register', () => {
  const wide = () => visibleColumns(defaultPrefs(), undefined, 'many-accounts')
  const floors = (columns: readonly ColumnDef[]) =>
    columns.reduce((sum, column) => sum + trackMinimumRem(column.track), 2)

  it('reads a fixed track and the floor of a minmax one', () => {
    expect(trackMinimumRem('7.75rem')).toBe(7.75)
    expect(trackMinimumRem('minmax(8.75rem, 1fr)')).toBe(8.75)
    expect(trackMinimumRem('minmax(0, 1fr)')).toBe(0)
  })

  it('keeps every column when the floors fit, returning the same array', () => {
    const columns = wide()
    expect(fitColumns(columns, floors(columns))).toBe(columns)
  })

  it('drops the least load-bearing columns first until the floors fit', () => {
    // A 1080p monitor with the drawer open: 1475px of register at 20px a rem.
    const fitted = fitColumns(wide(), 73.75)
    expect(floors(fitted)).toBeLessThanOrEqual(73.75)
    const ids = fitted.map((column) => column.id)
    expect(ids).not.toContain('notes')
    expect(ids).toContain('amount')
    expect(ids).toContain('payee')
    expect(ids).toContain('account')
  })

  it('never gives up the four a row is made of', () => {
    const ids = fitColumns(wide(), 10).map((column) => column.id)
    expect(ids).toEqual(['date', 'payee', 'category', 'amount'])
  })

  it('counts the selection checkbox against the width when asked', () => {
    const columns = wide()
    expect(fitColumns(columns, floors(columns), 4)).not.toBe(columns)
  })
})

/**
 * The widest strings were measured in the browser against this file's
 * longest fixtures. A floor is the text plus the cell's overhead, rounded up
 * to the next 0.25rem.
 */
describe('the text floors hold the strings they exist for', () => {
  const rem = (id: ColumnId) =>
    trackMinimumRem(COLUMNS.find((one) => one.id === id)?.track ?? '0') * 20

  // A cell pads 10px a side and the editor draws a 1px border inside that.
  const text = (id: ColumnId) => rem(id) - 22

  it('holds a 26-character payee such as "Example Federal Home Loans"', () => {
    expect(text('payee')).toBeGreaterThanOrEqual(230)
  })

  it('holds the longest account name, "Home Mortgage Loan"', () => {
    expect(text('account')).toBeGreaterThanOrEqual(177)
  })

  it('holds the longest category name, "Credit Card Payment"', () => {
    expect(text('category')).toBeGreaterThanOrEqual(170)
  })

  it('holds the longest date the column formats', () => {
    expect(text('date')).toBeGreaterThanOrEqual(113)
  })

  // A `LockedCell` draws a lock glyph beside its text; the glyph and its gap
  // take 21px that no other column spends.
  it('holds the longest statement name, lock glyph included', () => {
    expect(rem('statement_name') - 41).toBeGreaterThanOrEqual(330)
    expect(text('statement_name')).toBeGreaterThan(text('payee'))
  })

  it('spends no more than a quarter-rem above what each string needs', () => {
    expect(text('payee') - 230).toBeLessThan(5)
    expect(text('account') - 177).toBeLessThan(5)
    expect(text('category') - 170).toBeLessThan(5)
    expect(rem('statement_name') - 41 - 330).toBeLessThan(5)
  })
})

/** A 1920px window with the accounts drawer open: a 1431px register, 71.55rem. */
describe('fitting the default set to a 1920px register', () => {
  const drawerOpenRem = 1431 / 20
  const chosen = () => visibleColumns(defaultPrefs(), undefined, 'many-accounts')

  it('keeps the register in table mode rather than stacking its rows', () => {
    const kept = fitColumns(chosen(), drawerOpenRem)

    expect(floorsRem(kept) + 2).toBeLessThanOrEqual(drawerOpenRem)
    expect(kept.map((column) => column.id)).toContain('amount')
  })

  it('evicts the optional columns and keeps the four a row is made of', () => {
    const kept = fitColumns(chosen(), drawerOpenRem).map((column) => column.id)

    for (const id of ['date', 'payee', 'category', 'amount'] as const) {
      expect(kept).toContain(id)
    }
    expect(kept).not.toContain('statement_name')
  })

  // 1366 with the drawer open.
  it('stays in table mode on a 877px laptop register too', () => {
    const kept = fitColumns(chosen(), 877 / 20)

    expect(floorsRem(kept) + 2).toBeLessThanOrEqual(877 / 20)
    expect(kept.map((column) => column.id)).toEqual([
      'date',
      'flag',
      'reviewed',
      'payee',
      'category',
      'amount',
    ])
  })

  it('keeps the reviewed toggle on a 1366px laptop with the drawer open', () => {
    const kept = fitColumns(chosen(), 877 / 20).map((column) => column.id)

    expect(kept).toContain('reviewed')
    expect(kept).toContain('flag')
    expect(kept).not.toContain('account')
  })

  it('brings the statement name back on a register wide enough to draw it', () => {
    const kept = fitColumns(chosen(), 1998 / 20).map((column) => column.id)

    expect(kept).toContain('statement_name')
  })
})

describe('what Customize Columns says about a column that is on and absent', () => {
  const shown: readonly ColumnId[] = ['date', 'payee', 'category', 'amount']

  it('says nothing about a column that is on screen', () => {
    expect(hiddenReason('payee', shown)).toBeNull()
  })

  it('blames the width for a column the register could not fit', () => {
    expect(hiddenReason('statement_name', shown)).toBe('Hidden at this width')
  })

  // Dropped by `visibleColumns` for the account scope, never by `fitColumns`.
  it('gives the running balance its own reason', () => {
    expect(hiddenReason('balance', shown)).toBe('Only inside one account')
  })
})

/**
 * A 1366px laptop with the drawer open: an 877px register whose fixed tracks
 * (140 + 40 + 40 + 155 + 40) leave payee and category 462px. Each cell spends
 * 22px, measured in the browser.
 */
describe('the payee share of a laptop register', () => {
  const share = (id: ColumnId): number => {
    const track = COLUMNS.find((column) => column.id === id)?.track ?? ''
    return Number.parseFloat(/([\d.]+)fr/.exec(track)?.[1] ?? '0')
  }
  const overhead = 22
  const leftover = 462
  const text = (id: ColumnId) =>
    (leftover * share(id)) / (share('payee') + share('category')) - overhead

  it('gives the payee more of it than the category beside it', () => {
    expect(share('payee')).toBeGreaterThan(share('category'))
  })

  // A 26-character payee such as "Example Federal Home Loans" is 228px at this scale.
  it('holds a 26-character payee such as "Example Federal Home Loans"', () => {
    expect(text('payee')).toBeGreaterThanOrEqual(234)
  })

  it('does not take so much that the category starts clipping in its place', () => {
    expect(text('category')).toBeGreaterThanOrEqual(165)
  })
})

describe('customizableColumns', () => {
  it('offers every column where a tab draws every column', () => {
    expect(customizableColumns()).toHaveLength(COLUMNS.length)
  })

  it('offers only what the activity tabs can draw', () => {
    const ids = customizableColumns(ACTIVITY_COLUMNS).map((column) => column.id)
    expect(ids).toEqual([...ACTIVITY_COLUMNS])
    expect(ids).not.toContain('statement_name')
  })
})
