/**
 * The grid's frame, rendered with no window behind it. It virtualizes against
 * `window`, which a server render does not have, and must render anyway.
 */

import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { TooltipProvider } from '@/components/ui'
import { COLUMNS, orderedColumns } from '@/lib/transactions/columns'
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
    applySuggestion: noop,
    discardSuggestion: noop,
    decidingSuggestion: false,
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

  it('heads the icon-only columns with their icon, named for assistive tech', () => {
    const html = grid(['date', 'flag', 'reviewed', 'payee', 'category', 'attachment', 'amount'])

    for (const label of ['Flag', 'Reviewed', 'Attachment']) {
      expect(html).toContain(`<span class="visually-hidden">${label}</span>`)
    }
    expect(html.match(/register__th-icon/g)).toHaveLength(3)
    expect(html.match(/<svg[^>]*aria-hidden="true"/g)?.length).toBeGreaterThanOrEqual(3)
  })

  it('lays the head out in the order of the columns it is given', () => {
    const html = renderToStaticMarkup(
      <TooltipProvider>
        <RegisterGrid
          rows={[]}
          columns={orderedColumns(['category', 'payee', 'date', 'amount'])
            .filter((column) => ['date', 'payee', 'category', 'amount'].includes(column.id))}
          height="md"
          view={VIEW}
          loading={false}
          hasMore={false}
          onLoadMore={noop}
          empty={<p>Nothing here</p>}
        />
      </TooltipProvider>,
    )

    expect(html.indexOf('Category')).toBeLessThan(html.indexOf('Payee'))
    expect(html.indexOf('Payee')).toBeLessThan(html.indexOf('Date'))
  })
})
