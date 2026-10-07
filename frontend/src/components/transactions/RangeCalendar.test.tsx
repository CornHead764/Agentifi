/**
 * The two-month calendar draws two months, and draws the picked window.
 */

import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { TooltipProvider } from '@/components/ui'
import { RangeCalendar } from './RangeCalendar'

function render(range: { from: string | null; to: string | null }): string {
  return renderToStaticMarkup(
    <TooltipProvider>
      <RangeCalendar range={range} onPick={() => {}} today={new Date(2026, 7, 30)} />
    </TooltipProvider>,
  )
}

describe('the range calendar', () => {
  it('shows two months side by side, opening on last month', () => {
    const rendered = render({ from: null, to: null })
    expect(rendered).toContain('July 2026')
    expect(rendered).toContain('August 2026')
  })

  it('opens on the picked window and marks its edges and its middle', () => {
    const rendered = render({ from: '2026-05-04', to: '2026-05-08' })
    expect(rendered).toContain('May 2026')
    expect(rendered).toContain('June 2026')
    expect(rendered).toContain('range-cal__day--edge')
    expect(rendered).toContain('range-cal__day--in')
  })

  it('an open-ended pick marks only its edge', () => {
    const rendered = render({ from: '2026-05-04', to: null })
    expect(rendered).toContain('range-cal__day--edge')
    expect(rendered).not.toContain('range-cal__day--in')
  })
})
