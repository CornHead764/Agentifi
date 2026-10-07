/** Tested directly against a stubbed `matchMedia`: `react-dom/server` only reports the server snapshot. */

import { renderToStaticMarkup } from 'react-dom/server'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { matchesMedia, subscribeToMedia, useMediaQuery } from './useMediaQuery'

interface FakeList {
  matches: boolean
  listeners: Set<() => void>
}

/** A `window` with just enough `matchMedia` on it, plus the lists it handed out. */
function stubMatchMedia(matches: (query: string) => boolean) {
  const lists = new Map<string, FakeList>()
  vi.stubGlobal('window', {
    matchMedia: (query: string) => {
      const list: FakeList = lists.get(query) ?? { matches: matches(query), listeners: new Set() }
      lists.set(query, list)
      return {
        get matches() {
          return list.matches
        },
        addEventListener: (_event: string, listener: () => void) => list.listeners.add(listener),
        removeEventListener: (_event: string, listener: () => void) =>
          list.listeners.delete(listener),
      }
    },
  })
  return lists
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('reading a media query', () => {
  it('reports what the browser says about the query it was given', () => {
    stubMatchMedia((query) => query === '(max-width: 30rem)')
    expect(matchesMedia('(max-width: 30rem)')).toBe(true)
    expect(matchesMedia('(max-width: 48rem)')).toBe(false)
  })

  it('reports false where there is no window at all', () => {
    expect(matchesMedia('(max-width: 30rem)')).toBe(false)
  })

  it('reports false where the window has no matchMedia', () => {
    vi.stubGlobal('window', {})
    expect(matchesMedia('(max-width: 30rem)')).toBe(false)
  })
})

describe('subscribing to a media query', () => {
  it('is called when the query changes, and stops when unsubscribed', () => {
    const lists = stubMatchMedia(() => false)
    const onChange = vi.fn()

    const unsubscribe = subscribeToMedia('(max-width: 30rem)', onChange)
    const list = lists.get('(max-width: 30rem)')
    for (const listener of list?.listeners ?? []) listener()
    expect(onChange).toHaveBeenCalledTimes(1)

    unsubscribe()
    expect(list?.listeners.size).toBe(0)
  })

  it('unsubscribes cleanly where there is no matchMedia to subscribe to', () => {
    expect(() => subscribeToMedia('(max-width: 30rem)', vi.fn())()).not.toThrow()
  })
})

describe('the hook', () => {
  it('draws the wide layout for a renderer with no viewport', () => {
    stubMatchMedia(() => true)
    function Probe() {
      return <span>{String(useMediaQuery('(max-width: 30rem)'))}</span>
    }
    expect(renderToStaticMarkup(<Probe />)).toBe('<span>false</span>')
  })
})
