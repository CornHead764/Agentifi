import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { TooltipProvider } from './Tooltip'
import { WarningMark } from './WarningMark'

function render(node: Parameters<typeof renderToStaticMarkup>[0]): string {
  return renderToStaticMarkup(<TooltipProvider>{node}</TooltipProvider>)
}

describe('<WarningMark>', () => {
  it('is named by the sentence its tooltip shows, and is a tab stop', () => {
    const html = render(<WarningMark text="The newest quote is from 6 days ago." />)
    expect(html).toContain('role="img"')
    expect(html).toContain('aria-label="The newest quote is from 6 days ago."')
    expect(html).toContain('tabindex="0"')
  })

  it('leads its name with the word it draws', () => {
    const html = render(<WarningMark badge label="Incomplete" text="1 of 2 holdings has no cost basis." />)
    expect(html).toContain('aria-label="Incomplete: 1 of 2 holdings has no cost basis."')
    expect(html).toContain('badge--warning')
    expect(html).toContain('Incomplete</span>')
  })

  it('stays out of the tab order inside a control that is already one', () => {
    const html = render(<WarningMark text="Balance held." focusable={false} />)
    expect(html).not.toContain('tabindex')
  })
})
