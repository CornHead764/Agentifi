import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import type { AutopayRule } from '@/lib/clients/bills'

import { AutopayControls } from './AutopayControls'
import { switchedAutopay } from './draft'

function render(kind: AutopayRule, figure = ''): string {
  return renderToStaticMarkup(
    <AutopayControls kind={kind} figure={figure} invalid={false} onKind={() => {}} onFigure={() => {}} />,
  )
}

describe('the autopay switch', () => {
  it('reads off for a connection whose rule is none, and asks nothing more', () => {
    const html = render('none')
    expect(html).toContain('role="switch"')
    expect(html).toContain('aria-checked="false"')
    expect(html).toContain('a reminder to pay it yourself')
    expect(html).not.toContain('Pays')
    expect(html).not.toContain('Days before')
  })

  it('reads on for a connection with a rule, and shows the rule', () => {
    const html = render('on_due_date')
    expect(html).toContain('aria-checked="true"')
    expect(html).toContain('Pays')
    expect(html).not.toContain('Days before')
  })

  it('asks for the figure the rule needs', () => {
    expect(render('days_before_due', '5')).toContain('Days before')
    expect(render('day_of_month', '9')).toContain('Day of the month')
  })

  it('drives the one rule field: off is none, on starts at the due date', () => {
    expect(switchedAutopay(false)).toBe('none')
    expect(switchedAutopay(true)).toBe('on_due_date')
  })
})
