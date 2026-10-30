import { describe, expect, it } from 'vitest'

import { formatDate } from '@/lib/format'
import { parseMoney } from '@/lib/money'
import { recurrenceFor } from '@/lib/recurrence'

import {
  autopayNote,
  billerPaidNote,
  draftFromEdits,
  dueNote,
  editsFromSeries,
  editsFromSuggestion,
  isPayManually,
  occurrenceKey,
  occurrenceMarkers,
  patchFromEdits,
  reminderAccount,
  seriesAmountErrors,
  slotWindow,
  splitsBalance,
  startSplitting,
  type Occurrence,
  type OccurrenceBillLink,
  type Series,
  type Suggestion,
} from './upcoming'

/** A series as the list hands one to the editor: renamed, matching the bank. */
const SERIES: Series = {
  id: 'b0b7',
  account_id: 'acct-1',
  category_id: null,
  kind: 'subscription',
  description: 'SQ *COFFEE 0123 SPRINGFIELD ZZ',
  display_name: 'Coffee subscription',
  label: 'Coffee subscription',
  amount: parseMoney('-12.00'),
  currency: 'USD',
  recurrence: recurrenceFor('EVERY_MONTH', new Date(2026, 7, 3)),
  start_on: '2026-08-03',
  end_on: null,
  next_due_on: '2026-09-03',
  due_on: '2026-09-03',
  override_next_due_on: null,
  override_next_amount: null,
  auto_adjust_due_on: true,
  reminder_days: 3,
  match_criteria: 'auto',
  match_amount_min: null,
  match_amount_max: null,
  tag_ids: [],
  splits: [],
  is_active: true,
  annualized_amount: parseMoney('-144.00'),
  occurrences_per_year: 12,
}

describe('editsFromSeries', () => {
  it('seeds the name box from display_name, never from the label', () => {
    // `label` falls back to `description`; seeding from it would save the
    // bank's raw string as a display name.
    const edits = editsFromSeries({ ...SERIES, display_name: null, label: SERIES.description })

    expect(edits.name).toBe('')
    expect(edits.description).toBe(SERIES.description)
  })

  it('reads the amount back as a string, not a float', () => {
    expect(editsFromSeries(SERIES).amount).toBe('-12.00')
  })
})

describe('draftFromEdits', () => {
  it('takes the server default for a reminder lead time nobody typed', () => {
    const edits = editsFromSeries(SERIES)

    expect(draftFromEdits({ ...edits, reminder_days: '' }).reminder_days).toBeNull()
    expect(draftFromEdits(edits).reminder_days).toBe(3)
    expect(draftFromEdits({ ...edits, reminder_days: '7' }).reminder_days).toBe(7)
  })

  it('keeps a rename out of the matching text', () => {
    const draft = draftFromEdits({ ...editsFromSeries(SERIES), name: 'Beans' })

    expect(draft.display_name).toBe('Beans')
    expect(draft.description).toBe(SERIES.description)
  })

  it('falls back to the name when there is no matching text at all', () => {
    const edits = { ...editsFromSeries(SERIES), name: 'Gym', description: '' }

    expect(draftFromEdits(edits).description).toBe('Gym')
  })

  it('sends the amount band only for the criterion that reads it', () => {
    const banded = {
      ...editsFromSeries(SERIES),
      match_criteria: 'range' as const,
      match_amount_min: '10.00',
      match_amount_max: '14.00',
    }

    expect(draftFromEdits(banded).match_amount_min).toBe('10.00')
    expect(draftFromEdits({ ...banded, match_criteria: 'auto' }).match_amount_min).toBeNull()
    expect(draftFromEdits({ ...banded, match_criteria: 'auto' }).match_amount_max).toBeNull()
  })
})

describe('typed series amounts', () => {
  const typed = {
    ...editsFromSeries(SERIES),
    amount: '$1,250.50',
    match_criteria: 'range' as const,
    match_amount_min: '1,000',
    match_amount_max: '$1,250.50',
    override_next_amount: '1,000',
  }

  it('sends amounts typed with a dollar sign or thousands commas as plain amounts', () => {
    const draft = draftFromEdits(typed)
    expect(draft.amount).toBe('1250.50')
    expect(draft.match_amount_min).toBe('1000.00')
    expect(draft.match_amount_max).toBe('1250.50')

    const patch = patchFromEdits(typed)
    expect(patch.amount).toBe('1250.50')
    expect(patch.override_next_amount).toBe('1000.00')
    expect(seriesAmountErrors(typed)).toEqual({})
  })

  it('names each field that is not an amount', () => {
    expect(
      seriesAmountErrors({
        ...typed,
        amount: 'twelve',
        match_amount_min: 'ten',
        override_next_amount: 'some',
      }),
    ).toEqual({
      amount: '"twelve" is not an amount.',
      match_amount_min: '"ten" is not an amount.',
      override_next_amount: '"some" is not an amount.',
    })
  })

  it('ignores a band the criterion does not read', () => {
    expect(seriesAmountErrors({ ...typed, match_criteria: 'auto', match_amount_min: 'ten' })).toEqual(
      {},
    )
  })
})

describe('patchFromEdits', () => {
  it('round-trips the Bill Connect switch rather than defaulting it', () => {
    // A switch read back wrong would be turned off behind the user's back.
    const patch = patchFromEdits(editsFromSeries(SERIES))

    expect(patch.auto_adjust_due_on).toBe(true)
  })

  it('round-trips the next-occurrence overrides and clears an emptied one', () => {
    const overridden = {
      ...SERIES,
      override_next_due_on: '2026-09-10',
      override_next_amount: parseMoney('-18.50'),
    }
    const edits = editsFromSeries(overridden)

    expect(edits.override_next_due_on).toBe('2026-09-10')
    expect(edits.override_next_amount).toBe('-18.50')

    const patch = patchFromEdits(edits)
    expect(patch.override_next_due_on).toBe('2026-09-10')
    expect(patch.override_next_amount).toBe('-18.50')

    // Emptied means cleared, not left alone: the form holds the whole answer.
    const cleared = patchFromEdits({
      ...edits,
      override_next_due_on: null,
      override_next_amount: '',
    })
    expect(cleared.override_next_due_on).toBeNull()
    expect(cleared.override_next_amount).toBeNull()
  })

  it('sends the reminder lead time only when one was typed', () => {
    // A box cleared to nothing leaves the stored value alone.
    const edits = editsFromSeries(SERIES)

    expect(edits.reminder_days).toBe('3')
    expect(patchFromEdits(edits).reminder_days).toBe(3)
    expect('reminder_days' in patchFromEdits({ ...edits, reminder_days: '' })).toBe(false)
    expect(patchFromEdits({ ...edits, reminder_days: '5' }).reminder_days).toBe(5)
    expect('reminder_days' in patchFromEdits({ ...edits, reminder_days: 'soon' })).toBe(false)
  })

  it('round-trips a one-time series with a null frequency', () => {
    const once = {
      ...SERIES,
      recurrence: recurrenceFor('ONE_TIME', new Date(2026, 7, 3)),
      next_due_on: null,
    }

    expect(patchFromEdits(editsFromSeries(once)).recurrence?.frequency).toBeNull()
  })
})

const SUGGESTION: Suggestion = {
  signature: 'a1b2',
  account_id: 'acct-1',
  category_id: 'cat-9',
  kind: 'bill',
  description: 'GLACIER FITNESS 8821',
  display_name: 'Glacier Fitness',
  label: 'Glacier Fitness',
  amount: parseMoney('-42.00'),
  currency: 'USD',
  recurrence: recurrenceFor('EVERY_MONTH', new Date(2026, 8, 12)),
  start_on: '2026-09-12',
  occurrences: 4,
  first_seen: '2026-05-12',
  last_seen: '2026-08-12',
  confidence: 0.9,
  match_criteria: 'range',
  match_amount_min: parseMoney('-45.00'),
  match_amount_max: parseMoney('-39.00'),
  transaction_ids: ['t1', 't2', 't3', 't4'],
}

describe('editsFromSuggestion', () => {
  it('carries the cadence the history kept, not a monthly guess', () => {
    const edits = editsFromSuggestion(SUGGESTION)
    expect(edits.recurrence).toEqual(SUGGESTION.recurrence)
    expect(edits.start_on).toBe('2026-09-12')
    expect(edits.amount).toBe('-42.00')
  })

  // Seeding the name box from `label` would save the bank's raw wording as a
  // display name.
  it('keeps the two names apart', () => {
    const edits = editsFromSuggestion(SUGGESTION)
    expect(edits.name).toBe('Glacier Fitness')
    expect(edits.description).toBe('GLACIER FITNESS 8821')
  })

  it('brings the amount band across with the criterion that reads it', () => {
    const edits = editsFromSuggestion(SUGGESTION)
    expect(edits.match_criteria).toBe('range')
    expect(edits.match_amount_min).toBe('-45.00')
    expect(edits.match_amount_max).toBe('-39.00')
  })

  it('saves as a draft the server will accept', () => {
    const draft = draftFromEdits(editsFromSuggestion(SUGGESTION))
    expect(draft.description).toBe('GLACIER FITNESS 8821')
    expect(draft.display_name).toBe('Glacier Fitness')
    expect(draft.category_id).toBe('cat-9')
    expect(draft.amount).toBe('-42.00')
    expect(draft.end_on).toBeNull()
  })

  it('leaves a bandless suggestion without a band', () => {
    const edits = editsFromSuggestion({
      ...SUGGESTION,
      match_criteria: 'auto',
      match_amount_min: null,
      match_amount_max: null,
    })
    expect(edits.match_amount_min).toBe('')
    expect(draftFromEdits(edits).match_amount_min).toBeNull()
  })
})

describe('the transaction template a series carries', () => {
  const SPLIT_SERIES: Series = {
    ...SERIES,
    amount: parseMoney('-100.00'),
    tag_ids: ['tag-1'],
    splits: [
      { amount: parseMoney('-70.00'), category_id: 'cat-a', memo: '', tag_ids: [] },
      { amount: parseMoney('-30.00'), category_id: 'cat-b', memo: 'the rest', tag_ids: ['tag-2'] },
    ],
  }

  it('reads a stored split back into the grid', () => {
    const edits = editsFromSeries(SPLIT_SERIES)
    expect(edits.tag_ids).toEqual(['tag-1'])
    expect(edits.splits.map((one) => one.amount)).toEqual(['-70.00', '-30.00'])
    expect(edits.splits[1].memo).toBe('the rest')
    expect(edits.splits[1].tag_ids).toEqual(['tag-2'])
  })

  // Two lines read from one series must not share a React key, or typing in
  // one remounts the other.
  it('gives every line its own key', () => {
    const edits = editsFromSeries(SPLIT_SERIES)
    expect(new Set(edits.splits.map((one) => one.key)).size).toBe(2)
  })

  it('sends the split back the way the server takes it', () => {
    const draft = draftFromEdits(editsFromSeries(SPLIT_SERIES))
    expect(draft.tag_ids).toEqual(['tag-1'])
    expect(draft.splits).toEqual([
      { amount: '-70.00', category_id: 'cat-a', memo: '', tag_ids: [] },
      { amount: '-30.00', category_id: 'cat-b', memo: 'the rest', tag_ids: ['tag-2'] },
    ])
  })

  it('sends an empty template for a series that has none', () => {
    const draft = draftFromEdits(editsFromSeries(SERIES))
    expect(draft.splits).toEqual([])
    expect(draft.tag_ids).toEqual([])
    expect(patchFromEdits(editsFromSeries(SERIES)).splits).toEqual([])
  })

  // The ledger refuses a transaction whose parts do not sum to it.
  it('will not let an unbalanced split be saved', () => {
    const edits = editsFromSeries(SPLIT_SERIES)
    expect(splitsBalance(edits)).toBe(true)
    expect(splitsBalance({ ...edits, amount: '-120.00' })).toBe(false)
    expect(splitsBalance(editsFromSeries(SERIES))).toBe(true)
  })

  it('opens a new grid on the whole amount, so the first thing typed is the share', () => {
    const drafts = startSplitting({ ...editsFromSeries(SERIES), amount: '-40.00' })
    expect(drafts).toHaveLength(2)
    expect(drafts[0].amount).toBe('-40.00')
    expect(drafts[1].amount).toBe('')
  })

  it('carries no template over from a detected pattern', () => {
    const edits = editsFromSuggestion(SUGGESTION)
    expect(edits.tag_ids).toEqual([])
    expect(edits.splits).toEqual([])
  })
})

describe('occurrenceMarkers', () => {
  const toNumber = (amount: string) => Number(amount)
  const occurrence = (over: Partial<Occurrence>): Occurrence => ({
    series_id: 's1',
    account_id: 'a1',
    category_id: null,
    kind: 'bill',
    label: 'Mortgage',
    due_on: '2026-09-01',
    amount: parseMoney('-2345.67'),
    status: 'upcoming',
    pays_on: null,
    bill: null,
    bill_link: null,
    transaction_id: null,
    ...over,
  })

  it('keys each marker by the day it lands, on its own account only', () => {
    const markers = occurrenceMarkers(
      'a1',
      [
        occurrence({}),
        occurrence({ series_id: 's2', label: 'Brokerage Transfer', due_on: '2026-09-01' }),
        occurrence({ series_id: 's3', account_id: 'other', label: 'Not mine' }),
      ],
      toNumber as never,
    )
    expect(markers['2026-09-01']).toHaveLength(2)
    expect(markers['2026-09-01'].map((one) => one.label)).toEqual(['Mortgage', 'Brokerage Transfer'])
  })

  it('flags income for the badge and leaves a bill unflagged', () => {
    const markers = occurrenceMarkers(
      'a1',
      [occurrence({}), occurrence({ series_id: 's2', kind: 'income', due_on: '2026-09-14' })],
      toNumber as never,
    )
    expect(markers['2026-09-01'][0].emphasis).toBeUndefined()
    expect(markers['2026-09-14'][0].emphasis).toBe(true)
  })

  it('leaves a skipped occurrence off the chart', () => {
    const markers = occurrenceMarkers('a1', [occurrence({ status: 'skipped' })], toNumber as never)
    expect(markers).toEqual({})
  })

  it('stands an autopaid bill on the day the money leaves, not the day it is due', () => {
    // `domain.Occurrence.MovesOn` and `ProjectBalances` both read the payment
    // date, so the marker sits on the step, the 21st.
    const markers = occurrenceMarkers(
      'a1',
      [occurrence({ due_on: '2026-10-26', pays_on: '2026-10-21' })],
      toNumber as never,
    )
    expect(Object.keys(markers)).toEqual(['2026-10-21'])
    expect(markers['2026-10-26']).toBeUndefined()
  })

  it('keeps the due date in the label of a marker that has moved', () => {
    const markers = occurrenceMarkers(
      'a1',
      [occurrence({ due_on: '2026-10-26', pays_on: '2026-10-21' })],
      toNumber as never,
    )
    expect(markers['2026-10-21'][0].label).toBe(`Mortgage · due ${formatDate('2026-10-26', 'short')}`)
  })

  it('leaves the label alone when the two dates are the same day', () => {
    const markers = occurrenceMarkers(
      'a1',
      [occurrence({ due_on: '2026-10-26', pays_on: '2026-10-26' })],
      toNumber as never,
    )
    expect(markers['2026-10-26'][0].label).toBe('Mortgage')
  })

  it('stands a late payment on its payment date as well', () => {
    // `MovesOn` is the payment date whichever side of the due date it falls,
    // so a bill paid after it was due steps the balance late and marks late.
    const markers = occurrenceMarkers(
      'a1',
      [occurrence({ due_on: '2026-10-26', pays_on: '2026-10-29' })],
      toNumber as never,
    )
    expect(Object.keys(markers)).toEqual(['2026-10-29'])
    expect(markers['2026-10-29'][0].label).toBe(`Mortgage · due ${formatDate('2026-10-26', 'short')}`)
  })

  it('groups two occurrences that land on the same day however they got there', () => {
    // One bill due that day and one autopaying into it: the chart shows a
    // single step, so both markers belong to the one point.
    const markers = occurrenceMarkers(
      'a1',
      [
        occurrence({ due_on: '2026-10-21' }),
        occurrence({ series_id: 's2', label: 'Brokerage Transfer', due_on: '2026-10-26', pays_on: '2026-10-21' }),
      ],
      toNumber as never,
    )
    expect(markers['2026-10-21'].map((one) => one.label)).toEqual([
      'Mortgage',
      `Brokerage Transfer · due ${formatDate('2026-10-26', 'short')}`,
    ])
  })
})

describe('dueNote', () => {
  const slot = { due_on: '2026-10-26', pays_on: null as string | null, bill_link: null }

  it('says when the bill is due, for a marker standing on the payment date', () => {
    expect(dueNote({ ...slot, pays_on: '2026-10-21' })).toBe(
      `due ${formatDate('2026-10-26', 'short')}`,
    )
  })

  it('speaks for a late payment too, unlike its counterpart', () => {
    expect(dueNote({ ...slot, pays_on: '2026-10-29' })).toBe(
      `due ${formatDate('2026-10-26', 'short')}`,
    )
    expect(autopayNote({ ...slot, pays_on: '2026-10-29' })).toBeNull()
  })

  it('says nothing when there is one date, or the two are the same day', () => {
    expect(dueNote({ ...slot, pays_on: '2026-10-26' })).toBeNull()
    expect(dueNote(slot)).toBeNull()
  })
})

describe('billerPaidNote', () => {
  const bill = {
    id: 'bill-1',
    amount_due: parseMoney('75.00'),
    due_on: '2026-08-06',
    status: 'paid' as const,
    source: 'provider' as const,
    fetched_at: '2026-08-07T06:00:00Z',
    document_id: null,
  }

  it('speaks on a past-due slot the provider reports paid', () => {
    expect(billerPaidNote({ status: 'past_due', bill })).toBe('biller reports paid')
  })

  it('says nothing for an open statement, a settled slot, or no bill', () => {
    expect(billerPaidNote({ status: 'past_due', bill: { ...bill, status: 'open' } })).toBeNull()
    expect(billerPaidNote({ status: 'paid', bill })).toBeNull()
    expect(billerPaidNote({ status: 'past_due', bill: null })).toBeNull()
  })
})

describe('occurrenceKey', () => {
  const bill = {
    amount_due: parseMoney('50.00'),
    due_on: '2026-09-03',
    status: 'open' as const,
    source: 'provider' as const,
    fetched_at: '2026-09-01T06:00:00Z',
    document_id: null,
  }

  it('tells apart two bills due the same day on one reminder', () => {
    const slot = { series_id: 'ser-lawn', due_on: '2026-09-03' }
    const first = occurrenceKey({ ...slot, bill: { ...bill, id: 'bill-a' } })
    const second = occurrenceKey({ ...slot, bill: { ...bill, id: 'bill-b' } })
    expect(first).not.toBe(second)
  })

  it('is the slot alone when no bill speaks about it', () => {
    expect(occurrenceKey({ series_id: 'ser-lawn', due_on: '2026-09-03', bill: null })).toBe(
      'ser-lawn:2026-09-03',
    )
  })
})

describe('autopayNote', () => {
  const slot = {
    due_on: '2026-10-26',
    pays_on: null as string | null,
    bill_link: null as OccurrenceBillLink | null,
  }
  const link = (autopay: boolean): OccurrenceBillLink => ({
    connection_id: 'conn-clinic',
    biller: 'mychart',
    connection_label: 'Example Health',
    subaccount_label: 'Guarantor account ****5678',
    health: 'ok',
    autopay,
  })

  it('says when the money leaves, for a bill paid before it is due', () => {
    // Compared against the short date so the assertion is locale-independent.
    const note = autopayNote({ ...slot, pays_on: '2026-10-21' })
    expect(note).toBe(`autopays ${formatDate('2026-10-21', 'short')}`)
    expect(note?.startsWith('autopays ')).toBe(true)
  })

  it('says nothing when the payment lands on the due date', () => {
    expect(autopayNote({ ...slot, pays_on: '2026-10-26' })).toBeNull()
  })

  it('says nothing when the payment is after the due date, or unknown', () => {
    expect(autopayNote({ ...slot, pays_on: '2026-10-29' })).toBeNull()
    expect(autopayNote(slot)).toBeNull()
  })

  it('says to pay by hand a provider’s bill that no autopay takes', () => {
    expect(autopayNote({ ...slot, bill_link: link(false) })).toBe('pay manually')
    expect(autopayNote({ ...slot, bill_link: link(true) })).toBeNull()
  })

  it('lets a date the provider states speak over the missing rule', () => {
    expect(autopayNote({ ...slot, pays_on: '2026-10-21', bill_link: link(false) })).toBe(
      `autopays ${formatDate('2026-10-21', 'short')}`,
    )
  })
})

describe('a pay-manually reminder', () => {
  const statement = {
    series_id: null,
    account_id: null,
    due_on: '2026-08-28',
    bill: {
      id: 'bill-aug',
      amount_due: parseMoney('90.00'),
      due_on: '2026-08-28',
      status: 'open' as const,
      source: 'provider' as const,
      fetched_at: '2026-08-04T06:00:00Z',
      document_id: 'doc-aug',
    },
    bill_link: {
      connection_id: 'conn-clinic',
      biller: 'mychart',
      connection_label: 'Example Health',
      subaccount_label: 'Guarantor account ****5678',
      health: 'ok' as const,
      autopay: false,
    },
  }

  it('is a statement with no series, keyed by it', () => {
    expect(isPayManually(statement)).toBe(true)
    expect(isPayManually({ series_id: 'ser-power' })).toBe(false)
    expect(occurrenceKey(statement)).toBe('statement:2026-08-28:bill-aug')
  })

  it('names the billed account it is owed on, since no account pays it', () => {
    expect(reminderAccount(statement, () => 'Everyday Checking')).toBe('Guarantor account ****5678')
    expect(reminderAccount({ ...statement, account_id: 'acct-1' }, () => 'Everyday Checking')).toBe(
      'Everyday Checking',
    )
  })
})

describe('slotWindow', () => {
  it('spans the charge either way, so a bill paid late still sees both slots', () => {
    expect(slotWindow('2026-08-28')).toEqual({ from: '2026-07-14', to: '2026-10-12' })
  })

  it('falls back to today for a charge with no date', () => {
    const { from, to } = slotWindow(null)
    expect(from < to).toBe(true)
  })
})
