/** The phone's tab bar: every destination, with nothing behind a "More". */

import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { DESTINATIONS } from './destinations'
import { TabBar, type TabBarProps } from './TabBar'

function barAt(path: string, props: TabBarProps = {}): string {
  return renderToStaticMarkup(
    <MemoryRouter initialEntries={[path]}>
      <TabBar {...props} />
    </MemoryRouter>,
  )
}

/** The text of every `.tabbar__label`, in the order the bar draws them. */
function labels(html: string): string[] {
  return [...html.matchAll(/class="tabbar__label">([^<]*)</g)].map((match) => match[1])
}

describe('<TabBar>', () => {
  it('has a tab for every destination', () => {
    expect(labels(barAt('/'))).toEqual(['Home', 'Review', 'Activity', 'Plan', 'Insights'])
  })

  it('never draws a label that needs an ellipsis to fit a fifth of a phone', () => {
    for (const label of labels(barAt('/'))) {
      expect(label.length).toBeLessThanOrEqual('Activity'.length)
    }
  })

  it('marks the destination of a section current', () => {
    const marked = barAt('/goals').match(/<a[^>]*aria-current="page"[^>]*>/g) ?? []

    expect(marked).toHaveLength(1)
    expect(marked[0]).toContain('href="/spending-plan"')
  })

  it('draws the review count', () => {
    expect(barAt('/', { counts: { '/review': 3 } })).toContain('aria-label="Review, 3 to review"')
  })

  it('reaches every page through a destination', () => {
    const paths = DESTINATIONS.flatMap((destination) => [
      destination.path,
      ...destination.sections.map((one) => one.path),
    ])
    for (const path of [
      '/', '/review', '/transactions', '/rules', '/spending-plan', '/upcoming', '/goals',
      '/watchlist', '/planning-tools', '/reports', '/net-worth', '/investing',
    ]) {
      expect(paths).toContain(path)
    }
  })
})
