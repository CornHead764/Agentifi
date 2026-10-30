/**
 * The tag actions in the row's three-dot menu, and the dialog they open.
 */

import { describe, expect, it, vi } from 'vitest'

// Radix portals render nothing under static markup, so the menu is replaced
// with one that lists its entries inline, and the dialog shell with an inline
// one; the row menu and the tag dialog are the real components.
vi.mock('@/components/ui', async (importOriginal) => {
  const real = await importOriginal<typeof import('@/components/ui')>()
  return {
    ...real,
    ...(await import('@/test/dialogStub')).dialogStub,
    OverflowMenu: ({
      actions,
    }: {
      actions: readonly ({ label: string; onSelect: () => void } | null | false)[]
    }) => (
      <ul>
        {actions.flatMap((action, at) =>
          action && typeof action === 'object' && 'label' in action ? (
            <li key={at}>
              <button type="button" onClick={action.onSelect}>
                {action.label}
              </button>
            </li>
          ) : [],
        )}
      </ul>
    ),
  }
})

import { transaction } from '@/test/builders'
import { renderScreen } from '@/test/renderScreen'

import { RowMenu } from './RowMenu'
import { TagRowsDialog } from './TagRowsDialog'

const noop = () => undefined

const HANDLERS = {
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

describe('the row menu tag actions', () => {
  it('names the one row when nothing is selected', () => {
    const html = renderScreen(<RowMenu txn={transaction()} {...HANDLERS} onEditTags={noop} />)
    expect(html).toContain('Add tags…')
    expect(html).toContain('Remove tags…')
  })

  it('says how many selected rows it will reach', () => {
    const html = renderScreen(
      <RowMenu txn={transaction()} {...HANDLERS} onEditTags={noop} tagTargetCount={3} />,
    )
    expect(html).toContain('Add tags to 3 selected rows…')
    expect(html).toContain('Remove tags from 3 selected rows…')
  })

  it('leaves the actions out where the page does not handle them', () => {
    const html = renderScreen(<RowMenu txn={transaction()} {...HANDLERS} />)
    expect(html).not.toContain('Add tags')
  })
})

describe('the tag dialog', () => {
  const tags = [
    { id: 't1', name: 'Trip', color: null },
    { id: 't2', name: 'Tax', color: null },
  ] as never

  it('says how many rows it will tag, and offers the space’s tags', () => {
    const html = renderScreen(
      <TagRowsDialog
        mode="add"
        ids={['a', 'b', 'c'] as never}
        tags={tags}
        onClose={noop}
        onDone={noop}
      />,
    )
    expect(html).toContain('Add tags to 3 rows')
    expect(html).toContain('Trip')
    expect(html).toContain('Tax')
  })

  it('says it removes in remove mode', () => {
    const html = renderScreen(
      <TagRowsDialog mode="remove" ids={['a'] as never} tags={tags} onClose={noop} onDone={noop} />,
    )
    expect(html).toContain('Remove tags from 1 row')
  })
})
