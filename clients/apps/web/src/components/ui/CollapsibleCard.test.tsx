import { renderToStaticMarkup } from 'react-dom/server'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { CollapsibleCard } from './CollapsibleCard'

function fakeStorage(stored: string | null) {
  vi.stubGlobal('window', { localStorage: { getItem: () => stored } })
}

function render(startCollapsed?: boolean) {
  return renderToStaticMarkup(
    <CollapsibleCard
      title="Reminders"
      storageKey="reminders.collapsed"
      startCollapsed={startCollapsed}
      what="reminders"
    >
      <p>The body</p>
    </CollapsibleCard>,
  )
}

describe('a collapsible card', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('starts as its owner says when nothing has been stored', () => {
    fakeStorage(null)
    expect(render(true)).not.toContain('The body')
    expect(render(true)).toContain('aria-label="Show reminders"')
    expect(render()).toContain('The body')
    expect(render()).toContain('aria-expanded="true"')
  })

  it('stays open once somebody has opened it', () => {
    fakeStorage('0')
    expect(render(true)).toContain('The body')
  })

  it('stays shut once somebody has shut it', () => {
    fakeStorage('1')
    expect(render()).not.toContain('The body')
    expect(render()).toContain('Reminders')
  })

  it('falls back to its start rather than throwing when storage is refused', () => {
    vi.stubGlobal('window', {
      get localStorage(): never {
        throw new Error('blocked')
      },
    })
    expect(render(true)).not.toContain('The body')
  })
})
