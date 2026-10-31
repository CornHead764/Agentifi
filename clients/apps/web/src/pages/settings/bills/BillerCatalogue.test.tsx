import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { BILLERS } from '@/lib/billers'

import { BillerCatalogue } from './BillerCatalogue'

function render(): string {
  return renderToStaticMarkup(
    <MemoryRouter>
      <BillerCatalogue value={null} onChange={() => {}} onLeave={() => {}} />
    </MemoryRouter>,
  )
}

describe('the bill-provider catalogue', () => {
  it('lists every provider alphabetically in one group with none chosen', () => {
    const html = render()
    const names = BILLERS.filter((one) => one.generic !== true)
      .map((one) => one.name)
      .sort((a, b) => a.localeCompare(b))
    const positions = names.map((name) => html.indexOf(`<span>${name}</span>`))
    expect(positions.every((at) => at >= 0)).toBe(true)
    expect([...positions].sort((a, b) => a - b)).toEqual(positions)
    expect(html.match(/role="radiogroup"/g)).toHaveLength(2)
    expect(html).not.toContain('Invoice providers')
    expect(html).not.toContain('data-state="checked"')
  })

  it('offers a way out for a provider that is not listed', () => {
    const html = render()
    expect(html).toContain('Don’t see your provider?')
    expect(html).toContain('href="/upcoming/recurring?new=1"')
    expect(html).toContain('href="/settings/email#mail-rules"')
    expect(html).toContain('docs/connectors/adding-a-bill-provider.md')
  })

  it('offers a company with no connector as one tracked from its emails, not as a listing', () => {
    const html = render()
    expect(html).toContain('Track it from the bills it emails you')
    expect(html).toContain('value="email-only"')
    // Not among the searchable providers: it is no one company.
    expect(html).not.toContain('Emailed bills')
  })
})
