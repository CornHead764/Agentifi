import { describe, expect, it, vi } from 'vitest'

// Radix portals render nothing under static markup, so the dialog shell is
// replaced with an inline one; everything inside it is the real component.
vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).dialogStub,
}))

import { moneyFromCents } from '@/lib/money'
import { ACCOUNTS_KEY } from '@/lib/transactions/cache'
import { renderScreen } from '@/test/renderScreen'

import { EnvelopeTransactionsDialog } from './EnvelopeTransactionsDialog'
import { planEntry, planEnvelope } from './__fixtures__/plan'

function render(node: React.ReactNode): string {
  return renderScreen(node, {
    seed: [[ACCOUNTS_KEY, [{ id: 'acct-1', name: 'Everyday Checking' }]]],
  })
}

/**
 * The list has to add up to the header above it: a split receipt lists its
 * share, not the row's total.
 */
describe('the envelope transactions dialog', () => {
  const split = planEntry({
    id: 't-costco:s1',
    txn_id: 't-costco',
    name: 'Costco',
    due_on: '2026-08-12',
    status: 'paid',
    category_name: 'Groceries',
    amount: moneyFromCents(-24_000),
    account_id: 'acct-1',
    is_split: true,
    group: null,
  })
  const whole = planEntry({
    id: 't-aldi',
    txn_id: 't-aldi',
    name: 'Aldi',
    due_on: '2026-08-19',
    status: 'paid',
    category_name: 'Groceries',
    amount: moneyFromCents(-31_000),
    account_id: 'acct-1',
    group: null,
  })
  const envelope = planEnvelope({
    name: 'Groceries',
    spent: moneyFromCents(55_000),
    entries: [split, whole],
  })

  it('lists the share a split row gave the envelope, not the whole receipt', () => {
    const rendered = render(
      <EnvelopeTransactionsDialog envelope={envelope} month="2026-08" onOpenChange={() => undefined} />,
    )

    expect(rendered).toContain('240.00')
    expect(rendered).toContain('310.00')
    // The receipt's own total. It belongs to the row, not to this envelope.
    expect(rendered).not.toContain('340.00')
  })

  it('adds up to the spent figure in the header', () => {
    const rendered = render(
      <EnvelopeTransactionsDialog envelope={envelope} month="2026-08" onOpenChange={() => undefined} />,
    )

    const listed = envelope.entries.reduce((total, entry) => total - entry.amount, 0)
    expect(listed).toBe(envelope.spent)
    expect(rendered).toContain('550.00')
  })

  it('marks a part so a smaller figure than the receipt is explained', () => {
    const rendered = render(
      <EnvelopeTransactionsDialog envelope={envelope} month="2026-08" onOpenChange={() => undefined} />,
    )

    expect(rendered).toContain('split')
  })

  it('says so plainly when the envelope was charged nothing', () => {
    const rendered = render(
      <EnvelopeTransactionsDialog
        envelope={planEnvelope({ entries: [] })}
        month="2026-08"
        onOpenChange={() => undefined}
      />,
    )

    expect(rendered).toContain('Nothing in')
  })
})
