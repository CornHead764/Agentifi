/**
 * The phone's row as static markup. The gestures are tested in
 * lib/transactions/gestures; this pins that the menu is reachable and the row
 * is one button onto the transaction.
 */

import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { TooltipProvider } from '@/components/ui'
import { transaction } from '@/test/builders'

import { PhoneRow } from './PhoneRow'
import { RowMenu } from './RowMenu'
import { RegisterContext, type RegisterView } from './register-context'

const noop = () => undefined

const VIEW: RegisterView = {
  lookups: {
    accounts: [],
    categories: [],
    tags: [],
    accountName: () => 'Cashback Mastercard',
    categoryName: () => 'Fast Food',
    tagName: () => 'tag',
    frequentCategoryIds: [],
  },
  actions: {
    edit: noop,
    setReviewed: noop,
    setTags: noop,
    openDetail: noop,
    openReview: noop,
    applySuggestion: noop,
    discardSuggestion: noop,
    decidingSuggestion: false,
    toggleSplits: noop,
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
  expanded: new Set(),
}

function row(node = <PhoneRow txn={transaction()} />): string {
  return renderToStaticMarkup(
    <TooltipProvider>
      <RegisterContext.Provider value={VIEW}>{node}</RegisterContext.Provider>
    </TooltipProvider>,
  )
}

describe('a register row on a phone', () => {
  // A ⋮ button would spend a fifth of the row's width on an affordance only a
  // handful of rows out of a screenful want. A swipe reaches the same menu.
  it('draws no row-actions button', () => {
    const html = row(
      <PhoneRow
        txn={transaction()}
        menu={(control) => <RowMenu {...control} txn={transaction()} {...MENU_HANDLERS} />}
      />,
    )

    expect(html).not.toContain('Actions for')
    expect(html).toContain('row-menu__anchor')
  })

    // Radix positions against the trigger's box; a removed or display:none
    // trigger has none. It stays in the DOM, invisible and un-tabbable.
  it('leaves the menu an anchor to open against, out of the tab order', () => {
    const html = row(
      <PhoneRow
        txn={transaction()}
        menu={(control) => <RowMenu {...control} txn={transaction()} {...MENU_HANDLERS} />}
      />,
    )

    expect(html).toContain('aria-hidden="true"')
    expect(html).not.toContain('<button type="button" class="row-menu__anchor"')
  })

  it('is still one button onto the transaction', () => {
    expect(row()).toContain('txn-line')
    expect(row()).toContain('Harbor Coffee')
  })

  // Nothing is revealed until a finger moves: a lane painted at rest is a
  // second row of colour down a list that is meant to read as figures.
  it('shows no swipe lane until the row is being dragged', () => {
    expect(row()).not.toContain('txn-swipe__lane')
    expect(row()).toContain('txn-swipe__surface')
  })
})

const MENU_HANDLERS = {
  onEdit: noop,
  onReview: noop,
  onDelete: noop,
  onToggleExclusion: noop,
  onSetReviewed: noop,
  onCreateRule: noop,
  onCreateSeries: noop,
  onLinkSeries: noop,
  onUnlinkSeries: noop,
  onToggleBill: noop,
  onToggleSubscription: noop,
}
