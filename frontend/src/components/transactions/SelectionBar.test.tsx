/** The phone's selection bar: the count, the bulk actions and the way out, together. */

import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { SelectionBar, type SelectionBarProps } from './SelectionBar'

const noop = () => undefined

function bar(overrides: Partial<SelectionBarProps> = {}): string {
  return renderToStaticMarkup(
    <SelectionBar
      count={2}
      allReviewed={false}
      busy={false}
      onExit={noop}
      onMarkReviewed={noop}
      onSuggestCategories={noop}
      onCountTowardGoal={noop}
      {...overrides}
    />,
  )
}

describe('the selection bar', () => {
  it('says how many rows are about to be acted on', () => {
    expect(bar({ count: 1 })).toContain('1 selected')
    expect(bar({ count: 2 })).toContain('2 selected')
  })

  // Clearing the pressed row leaves the mode on at zero, which the bar must say.
  it('says nothing is selected rather than emptying itself', () => {
    expect(bar({ count: 0 })).toContain('0 selected')
  })

  it('offers the way out', () => {
    expect(bar()).toContain('aria-label="Exit selection"')
  })

  it('carries the bulk actions, each named for what it would act on', () => {
    const html = bar({ count: 3 })

    expect(html).toContain('aria-label="Mark 3 rows as reviewed"')
    expect(html).toContain('aria-label="Suggest categories for 3 rows"')
    expect(html).toContain('aria-label="Count 3 rows toward a goal"')
    expect(html).toContain('Mark reviewed')
  })

  it('unticks a set that is already ticked', () => {
    expect(bar({ allReviewed: true })).toContain('Mark unreviewed')
  })

  // An action with nothing to act on is not an action, but the X stays: it is
  // the only way out of the mode.
  it('offers no action while nothing is selected, and still offers the exit', () => {
    const html = bar({ count: 0 })

    expect(html.match(/disabled=""/g)).toHaveLength(3)
    expect(html).toContain('aria-label="Exit selection"')
    expect(html).not.toMatch(/aria-label="Exit selection"[^>]*disabled/)
  })

  it('refuses a second automation run while one is being queued', () => {
    expect(bar({ busy: true }).match(/disabled=""/g)).toHaveLength(1)
  })
})
