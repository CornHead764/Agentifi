import { afterEach, describe, expect, it, vi } from 'vitest'

import { auditUndeclaredMoney, coerceMoney } from './api'
import { moneyFromCents } from './money'
import { EMPTY_DRAFT, EMPTY_UNIVERSE, type FilterDraft } from './transactions/filter'
import type { FilterRead } from './transactions/types'
import {
  DETAIL_SHAPE,
  averageOfFullMonths,
  averageWindowLabel,
  blankWatchlistForm,
  breakdownFor,
  buildWatchlistBody,
  fullMonths,
  periodLabel,
  sharePercent,
  targetBarPct,
  watchlistFormFrom,
  type MonthSpend,
  type WatchlistDetail,
  type WatchlistForm,
  type WatchlistSummary,
} from './watchlists'

/** Thirteen bars ending in August 2026: twelve complete months, then the one still running. */
function trend(cents: number[], partialCents: number): MonthSpend[] {
  const months = cents.map((amount, index) => {
    const start = new Date(Date.UTC(2025, 7 + index, 1))
    return {
      month: `${start.getUTCFullYear()}-${String(start.getUTCMonth() + 1).padStart(2, '0')}`,
      spent: moneyFromCents(amount),
      is_partial: false,
    }
  })
  return [...months, { month: '2026-08', spent: moneyFromCents(partialCents), is_partial: true }]
}

const FULL_YEAR = [2_000, 3_000, 500, 4_500, 2_500, 1_500, 8_000, 0, 6_500, 8_000, 3_500, 2_000]

describe('the trailing monthly average', () => {
  it('leaves out the month in progress', () => {
    // A $9.00 partial month would drag a $35.00 average down to $33.00.
    const bars = trend(FULL_YEAR, 900)

    expect(fullMonths(bars)).toHaveLength(12)
    expect(fullMonths(bars).some((month) => month.is_partial)).toBe(false)
    expect(averageOfFullMonths(bars)).toBe(moneyFromCents(3_500))
  })

  it('does not move when the month in progress changes', () => {
    const early = averageOfFullMonths(trend(FULL_YEAR, 0))
    const later = averageOfFullMonths(trend(FULL_YEAR, 500_000))

    expect(early).toBe(later)
  })

  it('divides by the window, not by the months that had spending', () => {
    // A month with no matching rows is a month of zero spend.
    const bars = trend([1_200, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0], 900)

    expect(averageOfFullMonths(bars)).toBe(moneyFromCents(100))
  })

  it('divides by the history it has when there are fewer than twelve full months', () => {
    const bars: MonthSpend[] = [
      { month: '2026-06', spent: moneyFromCents(1_000), is_partial: false },
      { month: '2026-07', spent: moneyFromCents(3_000), is_partial: false },
      { month: '2026-08', spent: moneyFromCents(50), is_partial: true },
    ]

    expect(averageOfFullMonths(bars)).toBe(moneyFromCents(2_000))
  })

  it('is zero when nothing but the current month exists', () => {
    const bars: MonthSpend[] = [
      { month: '2026-08', spent: moneyFromCents(2_500), is_partial: true },
    ]

    expect(averageOfFullMonths(bars)).toBe(0)
    expect(averageWindowLabel(bars)).toBe('No full months yet')
  })

  it('names the window it covers, so nobody reads the current month into it', () => {
    expect(averageWindowLabel(trend(FULL_YEAR, 200))).toContain('12 full months')
    expect(averageWindowLabel(trend(FULL_YEAR, 200))).not.toContain('Aug 2026')
  })
})

/**
 * One watchlist as `GET /watchlists/{id}?month=2026-07` sends it, from
 * `TestTheBreakdownIsAboutTheSelectedMonthWhileTheCardIsAboutNow`.
 */
const WIRE_DETAIL = {
  id: '4bb15bd4-296a-43bf-9352-c9e9baf1fac1',
  name: 'Groceries',
  emoji: null,
  filter_id: 'f-groceries',
  period: 'monthly',
  this_month_spent: '50.00',
  month_projection: '77.50',
  year_to_date: '134.00',
  monthly_trend: [
    { month: '2026-07', spent: '12.00', is_partial: false },
    { month: '2026-08', spent: '50.00', is_partial: true },
  ],
  target_amount: '60.00',
  left_to_target: '10.00',
  pct_of_target: '83.33',
  is_over_target: false,
  is_projected_over_target: true,
  as_of: '2026-08-20',
  month: '2026-07',
  spent: '12.00',
  by_category: [{ key: 'cat-groceries', label: 'Groceries', spent: '12.00', share: '1' }],
  by_payee: [{ key: 'Corner Grocer', label: 'Corner Grocer', spent: '12.00', share: '1' }],
  by_tag: [{ key: '', label: '', spent: '12.00', share: '1' }],
}

function wireDetail(): WatchlistDetail {
  return coerceMoney<WatchlistDetail>(structuredClone(WIRE_DETAIL), DETAIL_SHAPE)
}

describe('a watchlist detail off the wire', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('reads the month the breakdown covers, which is not the card\'s month', () => {
    const detail = wireDetail()

    expect(detail.month).toBe('2026-07')
    expect(detail.spent).toBe(moneyFromCents(1_200))
    // The card half of the same response is still about now.
    expect(detail.this_month_spent).toBe(moneyFromCents(5_000))
  })

  it('takes each split from its own array rather than one merged list', () => {
    const detail = wireDetail()

    expect(breakdownFor(detail, 'category')[0].label).toBe('Groceries')
    expect(breakdownFor(detail, 'payee')[0].label).toBe('Corner Grocer')
    // The untagged slice has no name to print; the page supplies one.
    expect(breakdownFor(detail, 'tag')[0].label).toBe('')
    expect(breakdownFor(detail, 'category')[0].spent).toBe(moneyFromCents(1_200))
  })

  it('renders a share as the percentage of the fraction it is', () => {
    // The wire carries fractions.
    expect(sharePercent(breakdownFor(wireDetail(), 'category')[0])).toBe('100%')
    expect(sharePercent({ key: 'a', label: 'A', spent: moneyFromCents(1), share: '0.5' })).toBe(
      '50%',
    )
    expect(sharePercent({ key: 'a', label: 'A', spent: moneyFromCents(1), share: '0.3658' })).toBe(
      '37%',
    )
  })

  it('has no share to print when there was no spending to take a share of', () => {
    expect(sharePercent({ key: '', label: '', spent: moneyFromCents(0), share: null })).toBe('—')
  })

  it('declares every money field it is sent, so none reaches a chart as a string', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})

    auditUndeclaredMoney(wireDetail())

    // The rate is display-only and deliberately not coerced.
    const flagged = warn.mock.calls.map((call) => String(call[0]))
    expect(flagged).toHaveLength(1)
    expect(flagged[0]).toContain('pct_of_target')
  })
})

describe('the target bar', () => {
  it('fills to the share of the target the wire reports, in percent units', () => {
    // `pct_of_target` is already in percent units, unlike `share`.
    expect(targetBarPct(wireDetail())).toBeCloseTo(83.33)
  })

  it('stops at the end of the bar while the figures beside it do not', () => {
    const over = wireDetail()
    expect(targetBarPct({ ...over, pct_of_target: '240' })).toBe(100)
  })

  it('draws nothing when there is no target to be a share of', () => {
    expect(targetBarPct({ ...wireDetail(), pct_of_target: null })).toBe(0)
  })
})

describe('building the sheet body', () => {
  const named = (patch: Partial<WatchlistForm>): WatchlistForm => ({
    ...blankWatchlistForm(),
    name: 'Groceries',
    ...patch,
  })
  const groceries: FilterDraft = { ...EMPTY_DRAFT, categories: { ids: ['cat-1'], negated: false } }

  it('sends the trimmed name, the emoji, the target and the period beside the filter items', () => {
    const body = buildWatchlistBody(
      named({ name: '  Groceries  ', emoji: '🛒', target: '400.00', filter: groceries }),
      EMPTY_UNIVERSE,
    )
    expect(body).toMatchObject({
      name: 'Groceries',
      emoji: '🛒',
      target_amount: '400.00',
      period: 'month',
    })
    expect(body?.items).toHaveLength(1)
    expect(body?.items?.[0]).toMatchObject({
      field: 'category',
      operator: 'in',
      value_ids: ['cat-1'],
    })
    expect(body).not.toHaveProperty('filter_id')
  })

  it('sends blank emoji and target as null, so an edit that deletes them clears them', () => {
    expect(buildWatchlistBody(named({ filter: groceries }), EMPTY_UNIVERSE)).toMatchObject({
      emoji: null,
      target_amount: null,
    })
  })

  it('sends a target typed with a dollar sign or thousands commas as a plain amount', () => {
    for (const [typed, wire] of [
      ['$1,250.50', '1250.50'],
      ['1,000', '1000.00'],
    ]) {
      expect(
        buildWatchlistBody(named({ target: typed, filter: groceries }), EMPTY_UNIVERSE),
      ).toHaveProperty('target_amount', wire)
    }
  })

  it('is null with a target that is not an amount', () => {
    expect(
      buildWatchlistBody(named({ target: 'lots', filter: groceries }), EMPTY_UNIVERSE),
    ).toBe(null)
  })

  it('is null with no name, and with nothing picked, which would watch the whole ledger', () => {
    expect(buildWatchlistBody(named({ name: '   ', filter: groceries }), EMPTY_UNIVERSE)).toBe(null)
    expect(buildWatchlistBody(named({}), EMPTY_UNIVERSE)).toBe(null)
  })

  it('sends every facet picked, so one card can be groceries at one store', () => {
    const body = buildWatchlistBody(
      named({ filter: { ...groceries, payees: { values: ['Corner Grocer'], negated: false } } }),
      EMPTY_UNIVERSE,
    )
    expect(body?.items?.map((item) => item.field)).toEqual(['category', 'payee'])
  })

  it('sends the chosen period', () => {
    expect(
      buildWatchlistBody(named({ filter: groceries, period: 'year' }), EMPTY_UNIVERSE),
    ).toHaveProperty('period', 'year')
  })

  it('sends a saved report as its filter alone, and nothing until one is chosen', () => {
    const onReport = named({ source: 'report', filter: groceries })
    expect(buildWatchlistBody(onReport, EMPTY_UNIVERSE)).toBe(null)

    const body = buildWatchlistBody({ ...onReport, reportFilterId: 'flt-1' }, EMPTY_UNIVERSE)
    expect(body).toHaveProperty('filter_id', 'flt-1')
    expect(body).not.toHaveProperty('items')
  })
})

describe('reopening the sheet on a watchlist', () => {
  const card = (): WatchlistSummary => ({
    ...wireDetail(),
    name: 'Groceries',
    emoji: null,
    period: 'year',
    target_amount: moneyFromCents(40_000),
  })
  const item = (patch: Partial<FilterRead['items'][number]>): FilterRead['items'][number] => ({
    id: 'item-1',
    field: 'category',
    operator: 'in',
    group_index: 0,
    position: 0,
    negated: false,
    value_ids: ['cat-1'],
    value_texts: [],
    text: null,
    amount_min: null,
    amount_max: null,
    date_from: null,
    date_to: null,
    date_preset: null,
    state: null,
    ...patch,
  })

  it('fills the label, the target as typed text and the period', () => {
    const stored: FilterRead = {
      id: 'f-own',
      name: 'Groceries',
      scope: 'watchlist',
      query_text: null,
      items: [item({})],
    }
    const form = watchlistFormFrom(card(), stored, EMPTY_UNIVERSE)
    expect(form).toMatchObject({ name: 'Groceries', emoji: '', target: '400.00', period: 'year' })
  })

  it("brings the watchlist's own filter back as facets, and saves the same items", () => {
    const stored: FilterRead = {
      id: 'f-own',
      name: 'Groceries',
      scope: 'watchlist',
      query_text: null,
      items: [
        item({}),
        item({
          id: 'item-2',
          field: 'payee',
          position: 1,
          value_ids: [],
          value_texts: ['Corner Grocer'],
        }),
      ],
    }
    const form = watchlistFormFrom(card(), stored, EMPTY_UNIVERSE)
    expect(form.source).toBe('filter')
    expect(form.filter.categories.ids).toEqual(['cat-1'])
    expect(form.filter.payees.values).toEqual(['Corner Grocer'])

    const body = buildWatchlistBody(form, EMPTY_UNIVERSE)
    expect(body?.items).toEqual(stored.items.map((one) => ({ ...one, id: undefined })))
  })

  it("brings a report's filter back as that report, not as facets to edit", () => {
    const stored: FilterRead = {
      id: 'f-report',
      name: 'Groceries report',
      scope: 'report',
      query_text: null,
      items: [item({})],
    }
    const form = watchlistFormFrom(card(), stored, EMPTY_UNIVERSE)
    expect(form).toMatchObject({ source: 'report', reportFilterId: 'f-report' })
    expect(buildWatchlistBody(form, EMPTY_UNIVERSE)).toHaveProperty('filter_id', 'f-report')
  })
})

describe('the period the sheet offers', () => {
  it('prints one word for the two vocabularies that reach the card', () => {
    // `monthly` is what the server defaulted for older rows.
    expect(periodLabel('month')).toBe('Monthly')
    expect(periodLabel('monthly')).toBe('Monthly')
    expect(periodLabel('quarter')).toBe('Quarterly')
  })

  it('prints an unknown period as it was stored rather than dropping it', () => {
    expect(periodLabel('fortnight')).toBe('fortnight')
  })
})
