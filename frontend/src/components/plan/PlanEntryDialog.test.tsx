import type { QueryClient } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'

// Radix portals render nothing under static markup, so the dialog shell is
// replaced with an inline one; everything inside it is the real component.
vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).dialogStub,
}))

import type { SeriesHistory } from '@/lib/clients/upcoming'
import { moneyFromCents } from '@/lib/money'
import { ACCOUNTS_KEY } from '@/lib/transactions/cache'
import { renderScreen, testQueryClient } from '@/test/renderScreen'

import { PlanEntryDialog } from './PlanEntryDialog'
import { planEntry } from './__fixtures__/plan'

/**
 * The row's detail off a seeded cache: the transaction behind a posted row,
 * the months behind a recurring one, and the actions each offers.
 */
const noop = () => undefined

function render(node: React.ReactNode, seed: (client: QueryClient) => void): string {
  const client = testQueryClient()
  seed(client)
  return renderScreen(node, {
    client,
    seed: [[ACCOUNTS_KEY, [{ id: 'acct-1', name: 'Everyday Checking' }]]],
  })
}

const handlers = {
  month: '2026-08',
  isIncome: false,
  frozen: false,
  onOpenChange: noop,
  onExclude: noop,
  onEditSeries: noop,
  onLinkSeries: noop,
  onUnlinkSeries: noop,
  onCreateSeries: noop,
}

const history: SeriesHistory = {
  series: {
    id: 's1',
    account_id: 'acct-1',
    category_id: null,
    kind: 'bill',
    description: 'METRO ELECTRIC',
    display_name: 'Metro Electric',
    label: 'Metro Electric',
    amount: moneyFromCents(-30_000),
    currency: 'USD',
    recurrence: {
      alias: 'EVERY_MONTH',
      frequency: 'MONTHLY',
      interval: 1,
      by_month_day: [3],
      by_day: [],
      by_month: [],
    },
    start_on: '2026-01-03',
    end_on: null,
    next_due_on: '2026-09-03',
    due_on: '2026-09-03',
    override_next_due_on: null,
    override_next_amount: null,
    auto_adjust_due_on: false,
    reminder_days: 3,
    match_criteria: 'auto',
    match_amount_min: null,
    match_amount_max: null,
    tag_ids: [],
    splits: [],
    is_active: true,
    annualized_amount: moneyFromCents(-360_000),
    occurrences_per_year: 12,
  },
  from: '2025-09',
  to: '2026-08',
  rows: [
    {
      due_on: '2026-08-03',
      expected: moneyFromCents(-30_000),
      status: 'upcoming',
      off_schedule: false,
      transaction: null,
    },
    {
      due_on: '2026-07-03',
      expected: moneyFromCents(-30_000),
      status: 'paid',
      off_schedule: false,
      transaction: {
        id: 't-jul',
        account_id: 'acct-1',
        account_name: 'Everyday Checking',
        date: '2026-07-05',
        amount: moneyFromCents(-25_000),
        payee: 'Metro Electric',
        statement_name: 'METRO ELECTRIC DES:UTIL',
        category_id: null,
      },
    },
    {
      due_on: '2026-06-03',
      expected: moneyFromCents(-30_000),
      status: 'skipped',
      off_schedule: false,
      transaction: null,
    },
  ],
  paid_count: 1,
  average_paid: moneyFromCents(-25_000),
}

describe('the plan row detail', () => {
  it('shows a recurring row its months, what paid each and what that cost', () => {
    const html = render(
      <PlanEntryDialog
        {...handlers}
        entry={planEntry({
          id: 's1:2026-08-03',
          series_id: 's1',
          txn_id: null,
          status: 'upcoming',
        })}
      />,
      (client) => client.setQueryData(['series', 'history', 's1', '2026-08', 12], history),
    )
    expect(html).toContain('Recurring')
    expect(html).toContain('Every month')
    expect(html).toContain('averaged')
    expect(html).toContain('250.00')
    expect(html).toContain('Skipped')
    expect(html).toContain('Jul 5 · Everyday Checking')
    expect(html).toContain('highlight=t-jul')
    // An unfulfilled slot can be settled from here, and the series edited.
    expect(html).toContain('Mark as paid')
    expect(html).toContain('Skip this month')
    expect(html).toContain('Edit series')
    expect(html).toContain('Exclude from this month')
  })

  it('shows a posted row its transaction, a way to the register, and a way to a series', () => {
    const html = render(
      <PlanEntryDialog
        {...handlers}
        entry={planEntry({
          id: 't-1',
          txn_id: 't-1',
          series_id: null,
          status: 'paid',
          group: null,
        })}
      />,
      (client) =>
        client.setQueryData(['transaction', 't-1'], {
          id: 't-1',
          account_id: 'acct-1',
          date: '2026-08-14',
          amount: moneyFromCents(-4_500),
          payee: 'Costco',
          statement_name: 'COSTCO WHSE #0000',
          notes: null,
        }),
    )
    expect(html).toContain('Transaction')
    expect(html).toContain('Costco')
    expect(html).toContain('COSTCO WHSE #0000')
    expect(html).toContain('Everyday Checking')
    expect(html).toContain('Open in register')
    expect(html).toContain('highlight=t-1')
    expect(html).toContain('Link to a series')
    expect(html).toContain('Create a series from this')
    expect(html).not.toContain('Unlink from series')
  })

  it('lets a charge that filled a slot be moved to another series or released', () => {
    const html = render(
      <PlanEntryDialog
        {...handlers}
        entry={planEntry({
          id: 't-jul',
          txn_id: 't-jul',
          series_id: 's1',
          status: 'paid',
        })}
      />,
      (client) => {
        client.setQueryData(['series', 'history', 's1', '2026-08', 12], history)
        client.setQueryData(['transaction', 't-jul'], {
          id: 't-jul',
          account_id: 'acct-1',
          date: '2026-07-05',
          amount: moneyFromCents(-25_000),
          payee: 'Metro Electric',
          statement_name: 'METRO ELECTRIC DES:UTIL',
          notes: null,
        })
      },
    )
    expect(html).toContain('Link to a different series')
    expect(html).toContain('Unlink from series')
    expect(html).not.toContain('Mark as paid')
  })
})
