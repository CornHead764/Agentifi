/**
 * The crash screen at each scope.
 *
 * Server rendering does not run error boundaries, so the caught state is put
 * on an instance by hand and its fallback rendered as markup.
 */

import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { ErrorBoundary, type ErrorBoundaryScope } from './ErrorBoundary'

function fallback(scope: ErrorBoundaryScope, message: string): string {
  const boundary = new ErrorBoundary({ children: null, scope })
  boundary.state = ErrorBoundary.getDerivedStateFromError(new Error(message))
  return renderToStaticMarkup(<>{boundary.render()}</>)
}

describe('ErrorBoundary', () => {
  it('never shows the exception text', () => {
    for (const scope of ['app', 'page', 'panel'] as const) {
      const html = fallback(scope, "Cannot read properties of undefined (reading 'map')")
      expect(html).not.toContain('reading')
      expect(html).not.toContain('<pre')
    }
  })

  it('offers a retry in place below the app root, and a reload at it', () => {
    expect(fallback('panel', 'x')).toContain('Try again')
    expect(fallback('page', 'x')).toContain('Try again')
    expect(fallback('app', 'x')).toContain('Reload')
  })

  it('renders its children until something throws', () => {
    expect(renderToStaticMarkup(<ErrorBoundary scope="panel">fine</ErrorBoundary>)).toBe('fine')
  })

  it('clears a caught error when its reset key changes', () => {
    const boundary = new ErrorBoundary({ children: null, scope: 'page', resetKey: '/b' })
    boundary.state = { error: new Error('x') }
    let next: unknown = undefined
    boundary.setState = (state) => {
      next = state
    }
    boundary.componentDidUpdate({ children: null, scope: 'page', resetKey: '/a' })
    expect(next).toEqual({ error: null })
  })
})
