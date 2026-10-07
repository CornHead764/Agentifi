/**
 * The grid's frame, rendered with no window behind it. It virtualizes against
 * `window`, which a server render does not have, and must render anyway.
 */

import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { TooltipProvider } from '@/components/ui'
import { COLUMNS } from '@/lib/transactions/columns'
import type { ColumnId } from '@/lib/transactions/columns'

import { RegisterGrid } from './RegisterGrid'
import type { RegisterView } from './register-context'

const noop = () => undefined

const VIEW: RegisterView = {
  lookups: {
    accounts: [],
    categories: [],
    tags: [],
    accountName: () => 'Everyday Checking',
    categoryName: () => 'Uncategorized',
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
  multiAccount: false,
  expanded: new Set(),
}

function grid(
  only: readonly ColumnId[] = ['date', 'payee', 'category', 'amount'],
  view: RegisterView = VIEW,
): string {
  return renderToStaticMarkup(
    <TooltipProvider>
      <RegisterGrid
        rows={[]}
        columns={COLUMNS.filter((column) => only.includes(column.id))}
        height="md"
        view={view}
        loading={false}
        hasMore={false}
        onLoadMore={noop}
        empty={<p>Nothing here</p>}
      />
    </TooltipProvider>,
  )
}

/** The same view with its checkbox column on, and a selection in whatever state. */
function selecting(visibleIds: string[], ids: string[]): RegisterView {
  return {
    ...VIEW,
    selection: { ...VIEW.selection, enabled: true, visibleIds, ids: new Set(ids) },
  }
}

describe('<RegisterGrid>', () => {
  it('renders where there is no window to virtualize against', () => {
    const html = grid()

    expect(html).toContain('register__head')
    expect(html).toContain('register__viewport')
    expect(html).toContain('Payee')
    expect(html).toContain('Nothing here')
  })

  it('offers a select-all over the rows on screen, and says how many', () => {
    const html = grid(undefined, selecting(['a', 'b'], []))

    expect(html).toContain('aria-label="Select all 2 visible rows"')
    expect(html).toContain('data-state="unchecked"')
  })

  it('offers to clear instead once anything is selected', () => {
    const some = grid(undefined, selecting(['a', 'b'], ['a']))
    expect(some).toContain('aria-label="Clear the selection"')
    expect(some).toContain('data-state="indeterminate"')

    const all = grid(undefined, selecting(['a', 'b'], ['a', 'b']))
    expect(all).toContain('aria-label="Clear the selection"')
    expect(all).toContain('data-state="checked"')
  })

  it('draws no select-all where there is no checkbox column', () => {
    expect(grid()).not.toContain('Select all')
  })

  it('gives the list no scroll box of its own', () => {
    // Nothing may put a height back on the list inline: a second scroller is
    // what a tap on a phone's status bar cannot reach.
    expect(grid()).not.toMatch(/class="register__viewport"[^>]*style=/)
  })
})
