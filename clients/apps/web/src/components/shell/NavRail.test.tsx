import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { TooltipProvider } from '@/components/ui'

import { NavRail, type NavRailProps } from './NavRail'
import { DESTINATIONS, titleForPath } from './destinations'

function railAt(path: string, props: Partial<NavRailProps> = {}): string {
  return renderToStaticMarkup(
    <MemoryRouter initialEntries={[path]}>
      <TooltipProvider>
        <NavRail {...props} />
      </TooltipProvider>
    </MemoryRouter>,
  )
}

/** Every anchor in the render, as raw tag text. */
function anchors(html: string): string[] {
  return html.match(/<a\b[^>]*>/g) ?? []
}

function current(html: string): string[] {
  return anchors(html).filter((tag) => tag.includes('aria-current="page"'))
}

describe('<NavRail>', () => {
  it("renders every destination in Simplifi's order", () => {
    const hrefs = anchors(railAt('/')).map((tag) => /href="([^"]+)"/.exec(tag)?.[1])

    expect(hrefs).toEqual(DESTINATIONS.map((destination) => destination.path))
  })

  it('marks exactly one destination as the current page', () => {
    for (const destination of DESTINATIONS) {
      const marked = current(railAt(destination.path))

      expect(marked).toHaveLength(1)
      expect(marked[0]).toContain(`href="${destination.path}"`)
    }
  })

  it('does not mark the dashboard active on every other route', () => {
    // NavLink matches by prefix unless told otherwise, and "/" prefixes
    // everything.
    const marked = current(railAt('/transactions'))

    expect(marked[0]).toContain('href="/transactions"')
    expect(marked[0]).not.toContain('href="/"')
  })

  it('marks the register active for an account-scoped register', () => {
    const marked = current(railAt('/transactions?displayNode=shared-checking'))

    expect(marked).toHaveLength(1)
    expect(marked[0]).toContain('href="/transactions"')
  })

  it('leaves nothing marked on a route outside the rail', () => {
    expect(current(railAt('/settings/general'))).toHaveLength(0)
  })

  it('names every link on screen, so the icon is not the only clue', () => {
    const html = railAt('/')
    for (const destination of DESTINATIONS) {
      // "Bills & Income" reaches the markup with its ampersand escaped.
      const label = destination.label.replace(/&/g, '&amp;')
      expect(html).toContain(`<span class="rail__label">${label}</span>`)
    }
  })

  it('pins Refresh All at the bottom', () => {
    expect(railAt('/')).toContain('aria-label="Refresh all accounts"')
  })
})

describe('titleForPath', () => {
  it('names the header after the destination', () => {
    expect(titleForPath('/')).toBe('Dashboard')
    expect(titleForPath('/transactions')).toBe('Transactions')
    expect(titleForPath('/spending-plan')).toBe('Spending Plan')
  })

  it('names a destination`s sub-tab after the destination', () => {
    // Rules and guidance are two tabs of one place, and the header says the
    // place rather than changing under somebody switching tab.
    expect(titleForPath('/rules')).toBe('Rules')
    expect(titleForPath('/rules/guidance')).toBe('Rules')
  })

  it('names every settings section Settings', () => {
    expect(titleForPath('/settings/security')).toBe('Settings')
  })

    // Collapsed, the names stay in the markup for the stylesheet to hide, and
    // every link carries its name for a screen reader.
  it('names every destination to a screen reader whether or not it shows one', () => {
    for (const html of [railAt('/'), railAt('/', { collapsed: true })]) {
      for (const destination of DESTINATIONS) {
        expect(html).toContain(`aria-label="${destination.label.replace(/&/g, '&amp;')}"`)
      }
    }
  })

  it('offers no collapse control when the shell does not handle it', () => {
    const html = railAt('/')
    expect(html).not.toContain('Collapse sidebar')
    expect(html).toContain('<div class="rail__brand" aria-hidden="true">')
  })

  it('makes the logo the collapse control, both ways round', () => {
    const toggle = (props: Partial<NavRailProps>) =>
      /<button[^>]*class="rail__brand"[^>]*>/.exec(railAt('/', { onToggleCollapsed: () => undefined, ...props }))?.[0]

    expect(toggle({})).toContain('aria-label="Collapse sidebar"')
    expect(toggle({})).toContain('aria-expanded="true"')
    expect(toggle({ collapsed: true })).toContain('aria-label="Expand sidebar"')
    expect(toggle({ collapsed: true })).toContain('aria-expanded="false"')
  })

  it('has no second collapse button', () => {
    const html = railAt('/', { onToggleCollapsed: () => undefined })
    expect(html.match(/aria-expanded=/g)).toHaveLength(1)
  })
})
