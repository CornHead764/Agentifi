/**
 * The dialog a transaction is reviewed in: the register's edit form with a
 * banner on top. The proposal must be on screen in words beside its reason,
 * and the form underneath must be the same form.
 */

import { describe, expect, it, vi } from 'vitest'

// Radix portals render nothing under static markup, so the dialog shell is
// replaced with an inline one; everything inside it is the real component.
vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).dialogStub,
}))

import { moneyFromCents } from '@/lib/money'
import type {
  Account,
  Suggestion,
  Transaction,
  Uuid,
} from '@/lib/transactions/types'
import { category, transaction } from '@/test/builders'
import { renderScreen } from '@/test/renderScreen'

import { TransactionDialog, type ReviewFlow } from './TransactionDialog'

const ACCOUNT = {
  id: 'a1' as Uuid,
  name: 'Everyday Checking',
  kind: 'cash',
  type: 'checking',
  currency: 'USD',
  connection_id: null,
  is_closed: false,
} as unknown as Account

const GIFT_CARD = { ...ACCOUNT, id: 'gc' as Uuid, name: 'Amazon Gift Card', type: 'gift_card' }
const SYNCED = {
  ...ACCOUNT,
  id: 'sy' as Uuid,
  name: 'Synced Savings',
  connection_id: 'c1' as Uuid,
}

const CATEGORIES = [category('groceries', 'Groceries'), category('pet', 'Pet Supplies')]

const SUGGESTION: Suggestion = {
  action_id: 'p1' as Uuid,
  conversation_id: 'c1' as Uuid,
  run_id: 'r1' as Uuid,
  tool: 'update_transaction',
  summary: 'Costco is Groceries, as the last 11 times',
  category_id: 'groceries' as Uuid,
  splits: [],
  created_at: '2026-09-04T12:00:08Z',
}

const noop = () => undefined

function review(overrides: Partial<ReviewFlow> = {}): ReviewFlow {
  return {
    suggestion: SUGGESTION,
    hasNext: true,
    busy: false,
    onApprove: noop,
    onUseMine: noop,
    onDiscard: noop,
    onSkip: noop,
    onMarkReviewed: noop,
    onShowRun: noop,
    ...overrides,
  }
}

function render(options: {
  transaction: Transaction | null
  review?: ReviewFlow | null
  accounts?: readonly Account[]
  onOpenRow?: (id: Uuid) => void
}): string {
  return renderScreen(
    <TransactionDialog
      open
      onOpenChange={noop}
      transaction={options.transaction}
      accounts={options.accounts ?? [ACCOUNT]}
      categories={CATEGORIES}
      tags={[]}
      frequentCategoryIds={[]}
      defaultAccountId={null}
      spaceCurrency="USD"
      saving={false}
      onCreate={noop}
      onUpdate={noop}
      onDelete={noop}
      onSaveSplits={noop}
      onInvalid={noop}
      onCreateRule={noop}
      onCreateSeries={noop}
      onTrackRefund={noop}
      onMerchantOrder={noop}
      onOpenRow={options.onOpenRow}
      review={options.review ?? null}
    />,
  )
}

describe('reviewing a transaction with a suggestion', () => {
  it('says what the change would do and why, and offers the run behind it', () => {
    const html = render({ transaction: transaction({ suggestion: SUGGESTION }), review: review() })

    expect(html).toContain('Review transaction')
    expect(html).toContain('File this under Groceries')
    expect(html).toContain('Costco is Groceries, as the last 11 times')
    expect(html).toContain('Why? See the run')
  })

  it('offers approve, discard and skip, and names the next row in the button', () => {
    const html = render({ transaction: transaction({ suggestion: SUGGESTION }), review: review() })

    expect(html).toContain('Approve &amp; next')
    expect(html).toContain('Discard suggestion')
    expect(html).toContain('Skip')
  })

  it('drops the "next" wording once this is the last row worth reviewing', () => {
    const html = render({
      transaction: transaction({ suggestion: SUGGESTION }),
      review: review({ hasNext: false }),
    })

    expect(html).toContain('Approve')
    expect(html).not.toContain('Approve &amp; next')
    expect(html).not.toContain('Skip')
  })

  it('offers a change of category beside Approve, whatever the form below says', () => {
    // On a phone the form's Category field is a scroll away; the proposal is
    // changed where it is approved.
    for (const category_id of [null, 'pet' as Uuid, 'groceries' as Uuid]) {
      const html = render({
        transaction: transaction({ category_id, suggestion: SUGGESTION }),
        review: review(),
      })
      expect(html).toMatch(/Approve &amp; next[\s\S]*Change category[\s\S]*Discard suggestion/)
      expect(html).not.toMatch(/Use [^<]* instead/)
    }
  })

  it('leaves a proposed split to its own lines, with no single category to change', () => {
    const html = render({
      transaction: transaction({ suggestion: SUGGESTION }),
      review: review({
        suggestion: {
          ...SUGGESTION,
          tool: 'split_transaction',
          category_id: null,
          splits: [{ amount: moneyFromCents(-1_250), category_id: 'pet' as Uuid, memo: '' }],
        },
      }),
    })
    expect(html).not.toContain('Change category')
  })

  it('names every part of a proposed split with its amount and its item', () => {
    const html = render({
      transaction: transaction({ suggestion: SUGGESTION }),
      review: review({
        suggestion: {
          ...SUGGESTION,
          tool: 'split_transaction',
          category_id: null,
          splits: [
            { amount: moneyFromCents(-3_000), category_id: 'pet' as Uuid, memo: 'Dog food' },
            {
              amount: moneyFromCents(-4_000),
              category_id: 'groceries' as Uuid,
              memo: 'Batteries',
            },
          ],
        },
      }),
    })

    expect(html).toContain('Split into 2 categories')
    expect(html).toContain('Dog food')
    expect(html).toContain('Batteries')
    expect(html).toContain('Pet Supplies')
  })

  it('is the same edit form underneath, not a second editor', () => {
    const html = render({ transaction: transaction({ suggestion: SUGGESTION }), review: review() })

    expect(html).toContain('Payee')
    expect(html).toContain('Effective date')
    expect(html).toContain('Exclude from')
    expect(html).toContain('Update')
    expect(html).toContain('Delete transaction')
  })

  it('offers only the tick on a row nothing is waiting on', () => {
    const html = render({ transaction: transaction(), review: review({ suggestion: null }) })

    expect(html).toContain('Nothing is waiting on this transaction')
    expect(html).toContain('Mark reviewed &amp; next')
    expect(html).not.toContain('Discard suggestion')
  })

  it('does not put the review banner on a row somebody opened to edit', () => {
    const html = render({ transaction: transaction({ suggestion: SUGGESTION }) })

    expect(html).toContain('Transaction detail')
    expect(html).not.toContain('Approve')
  })
})

describe('where a new transaction may be filed', () => {
  it('leaves out the accounts a hand-entered row does not belong in', () => {
    const html = render({ transaction: null, accounts: [ACCOUNT, GIFT_CARD, SYNCED] })

    expect(html).toContain('Everyday Checking')
    expect(html).not.toContain('Amazon Gift Card')
    expect(html).not.toContain('Synced Savings')
  })

  it('keeps an existing row in its own account, however ineligible', () => {
    // Otherwise the account field would read as some other account and saving
    // the row would move it.
    const html = render({
      transaction: transaction({ account_id: SYNCED.id, suggestion: null }),
      accounts: [ACCOUNT, GIFT_CARD, SYNCED],
    })

    expect(html).toContain('Synced Savings')
    expect(html).not.toContain('Amazon Gift Card')
  })
})

describe('a pair a padding mail rule wrote', () => {
  it('names the income row on the purchase, with a way to open it', () => {
    const html = render({
      transaction: transaction({ padding_txn_id: 'pad-1' as Uuid }),
      onOpenRow: noop,
    })

    expect(html).toContain('Paid by payroll deduction')
    expect(html).toContain('Open the income row')
  })

  it('names the purchase on the income row', () => {
    const html = render({
      transaction: transaction({ amount: moneyFromCents(600), padded_txn_id: 'lunch-1' as Uuid }),
      onOpenRow: noop,
    })

    expect(html).toContain('recorded as income')
    expect(html).toContain('Open the purchase')
  })

  it('says nothing on a row that is neither', () => {
    expect(render({ transaction: transaction(), onOpenRow: noop })).not.toContain('payroll')
  })
})

describe('a row on an account that requires receipts', () => {
  it('offers to say no receipt is needed only where one is owed or was waived', () => {
    expect(render({ transaction: transaction({ receipt_status: 'missing' }) })).toContain(
      'No receipt needed',
    )
    expect(
      render({ transaction: transaction({ receipt_status: 'not_needed', receipt_not_needed: true }) }),
    ).toContain('No receipt needed')
    for (const status of ['on_file', null] as const) {
      expect(render({ transaction: transaction({ receipt_status: status }) })).not.toContain(
        'No receipt needed',
      )
    }
  })
})
