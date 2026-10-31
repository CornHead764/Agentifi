/** The phone's tab bar, the one place a destination reads `short` rather than its own name. */

import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { DESTINATIONS } from './destinations'
import { TabBar } from './TabBar'

function barAt(path: string): string {
  return renderToStaticMarkup(
    <MemoryRouter initialEntries={[path]}>
      <TabBar />
    </MemoryRouter>,
  )
}

/** The text of every `.tabbar__label`, in the order the bar draws them. */
function labels(html: string): string[] {
  return [...html.matchAll(/class="tabbar__label">([^<]*)</g)].map((match) => match[1])
}

describe('<TabBar>', () => {
  it('labels its tabs with the short form where there is one', () => {
    expect(labels(barAt('/'))).toEqual(['Home', 'Transactions', 'Plan', 'Bills', 'More'])
  })

  it('never draws a label that needs an ellipsis to fit a fifth of a phone', () => {
    for (const label of labels(barAt('/'))) {
      expect(label.length).toBeLessThanOrEqual('Transactions'.length)
      expect(label).not.toContain('…')
    }
  })

  it('leaves the full names on the destinations themselves', () => {
    const byPath = new Map(DESTINATIONS.map((one) => [one.path, one]))

    expect(byPath.get('/spending-plan')?.label).toBe('Spending Plan')
    expect(byPath.get('/upcoming')?.label).toBe('Bills & Income')
  })
})
