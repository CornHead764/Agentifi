/**
 * The refund picker: the credit on top, the charges it might be giving back
 * under it, and whatever it already gives back above them.
 */

import { describe, expect, it, vi } from 'vitest'

// Radix portals render nothing under static markup, so the dialog shell is
// replaced with an inline one; everything inside it is the real component.
vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).dialogStub,
}))

import { refundCandidatesKey, refundLinksKey, type RefundCharge } from '@/lib/clients/refunds'
import { parseMoney } from '@/lib/money'
import type { Transaction, Uuid } from '@/lib/transactions/types'
import { renderScreen } from '@/test/renderScreen'
import { LinkRefundDialog } from './LinkRefundDialog'

const CREDIT = {
  id: 't1',
  account_id: 'a1',
  date: '2026-09-09',
  amount: parseMoney('20.00'),
  payee: 'Fresh Market',
  statement_name: 'FRESH MKT REFUND',
  category_id: null,
} as unknown as Transaction

function charge(over: Partial<RefundCharge> = {}): RefundCharge {
  return {
    id: 'c1' as Uuid,
    account_id: 'a1' as Uuid,
    account_name: 'Everyday Checking',
    date: '2026-09-02',
    amount: parseMoney('-50.00'),
    payee: 'Fresh Market',
    statement_name: 'FRESH MKT 0912',
    category_id: 'cat-groceries' as Uuid,
    category_name: 'Groceries',
    ...over,
  }
}

function render(candidates: RefundCharge[], linked: RefundCharge[] = []): string {
  return renderScreen(<LinkRefundDialog txn={CREDIT} onClose={() => {}} onLink={() => {}} />, {
    seed: [
      [refundLinksKey(CREDIT.id), { can_be_a_refund: true, refunds: linked, refunded_by: [] }],
      [refundCandidatesKey(CREDIT.id, ''), { candidates }],
    ],
  })
}

describe('the refund picker', () => {
  it('names the credit being filed and the charges it might give back', () => {
    const rendered = render([
      charge(),
      charge({ id: 'c2' as Uuid, payee: 'Hardware Store', category_name: 'Home' }),
    ])
    expect(rendered).toContain('What does this refund?')
    expect(rendered).toContain('Sep 9, 2026')
    // The clean name, not the bank's wording — ground rule 5. The statement
    // name is only the fallback for a row nothing has renamed.
    expect(rendered).toContain('Fresh Market')
    expect(rendered).not.toContain('FRESH MKT 0912')
    expect(rendered).toContain('Hardware Store')
    expect(rendered).toContain('Everyday Checking')
    expect(rendered).toContain('Groceries')
  })

  // The whole point stated on the screen: the credit reduces a category rather
  // than counting as income.
  it('says which category a linked credit comes off', () => {
    const rendered = render([], [charge()])
    expect(rendered).toContain('Refunds')
    expect(rendered).toContain('rather than counting as income')
    expect(rendered).toContain('Fresh Market')
    expect(rendered).toContain('Groceries')
    expect(rendered).toContain('Release')
  })

  // A row in both lists reads as one row, and linking it twice says nothing new.
  it('does not offer a charge it is already linked to', () => {
    const linked = charge({ payee: 'Fresh Market Hall' })
    const other = charge({ id: 'c2' as Uuid, payee: 'Hardware Store' })

    const unlinked = render([linked, other])
    expect(unlinked.split('Fresh Market Hall').length - 1).toBe(1)

    // Once it is linked it appears once: above, as a link to release, and not
    // again in the offer below.
    const withLink = render([linked, other], [linked])
    expect(withLink.split('Fresh Market Hall').length - 1).toBe(1)
    expect(withLink).toContain('Release')
    expect(withLink).toContain('Hardware Store')
  })

  it('tells the user to search when nothing near the credit is big enough', () => {
    const rendered = render([])
    expect(rendered).toContain('Search to look further back')
  })
})
