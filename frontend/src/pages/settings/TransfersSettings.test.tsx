import type { QueryClient } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'
import { TRANSFERS_KEY, TRANSFER_ORPHANS_KEY, type TransferList } from '@/lib/clients/transfers'
import { renderScreen, testQueryClient } from '@/test/renderScreen'
import { TransfersSettings } from './TransfersSettings'

/** The screen, rendered off a seeded cache. The wording is what is asserted. */
function render(seed?: (client: QueryClient) => void) {
  const client = testQueryClient()
  seed?.(client)
  return renderScreen(<TransfersSettings />, { client })
}

const window = { from: null, to: null, date_field: 'posted' }

const paired: TransferList = {
  window,
  orphan_count: 0,
  transfers: [
    {
      pair_id: 'pair-1',
      moved_on: '2026-08-10',
      amount: moneyFromCents(20_000),
      currency: 'USD',
      paired_by_hand: false,
      from: {
        transaction_id: 'txn-out',
        account_id: 'acct-checking',
        account_name: 'Everyday Checking',
        date: '2026-08-10',
        amount: moneyFromCents(-20_000),
        currency: 'USD',
        payee: 'Transfer to Rewards Card',
        source: 'sync',
        pair_id: 'pair-1',
      },
      to: {
        transaction_id: 'txn-in',
        account_id: 'acct-card',
        account_name: 'Rewards Card',
        date: '2026-08-10',
        amount: moneyFromCents(20_000),
        currency: 'USD',
        payee: 'Payment from Checking',
        source: 'sync',
        pair_id: 'pair-1',
      },
    },
  ],
}

describe('the transfer activity screen', () => {
  it('renders with no server behind it', () => {
    const markup = render()
    expect(markup).toContain('Transfer activity')
  })

  it('says what a pair costs, since no other screen shows it', () => {
    expect(render()).toContain('left out of income and expense')
  })

  it('names both accounts and marks how the pair was made', () => {
    const markup = render((client) => client.setQueryData(TRANSFERS_KEY, paired))
    expect(markup).toContain('Everyday Checking')
    expect(markup).toContain('Rewards Card')
    expect(markup).toContain('Automatically')
    expect(markup).toContain('Unpair')
  })

  it('marks a hand-made pair differently from a guess', () => {
    const byHand: TransferList = {
      ...paired,
      transfers: [{ ...paired.transfers[0], paired_by_hand: true }],
    }
    expect(render((client) => client.setQueryData(TRANSFERS_KEY, byHand))).toContain('By hand')
  })

    // An inline glyph before the name is its own line-break opportunity.
  it('keeps the arrow inside the leg it points at', () => {
    const markup = render((client) => client.setQueryData(TRANSFERS_KEY, paired))
    const leg = markup.indexOf('transfer-leg')
    expect(leg).toBeGreaterThan(-1)
    expect(markup.indexOf('<svg', leg)).toBeLessThan(markup.indexOf('Rewards Card', leg))
  })

  it('stacks a transfer on a phone so the amount and Unpair stay on screen', () => {
    const markup = render((client) => client.setQueryData(TRANSFERS_KEY, paired))
    expect(markup).toContain('table--stack')
    expect(markup).toContain('data-label="Amount"')
    expect(markup).toContain('data-label="Paired"')
  })

  it('shows no orphan card for a healthy household', () => {
    const markup = render((client) => client.setQueryData(TRANSFERS_KEY, paired))
    expect(markup).not.toContain('half-paired')
  })

  it('names the damage when a leg has lost its partner', () => {
    const damaged: TransferList = { ...paired, orphan_count: 1 }
    const orphan = { ...paired.transfers[0].from, pair_id: 'pair-gone' }
    const markup = render((client) => {
      client.setQueryData(TRANSFERS_KEY, damaged)
      client.setQueryData(TRANSFER_ORPHANS_KEY, { orphans: [orphan] })
    })

    expect(markup).toContain('1 half-paired transfer')
    // The consequence, not the jargon: this is why the number on a report is
    // wrong, and why the row will never match again on its own.
    expect(markup).toContain('left out of every report')
    expect(markup).toContain('cannot be matched again')
    expect(markup).toContain('Release them')
    expect(markup).toContain('Transfer to Rewards Card')
  })
})
