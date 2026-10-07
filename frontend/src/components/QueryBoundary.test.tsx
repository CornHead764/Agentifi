/** The failed panel shows no status number, no path, nothing that reads like a stack trace. */

import { renderToStaticMarkup } from 'react-dom/server'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'

import { LoadFailure, QueryBoundary } from './QueryBoundary'

afterEach(() => vi.restoreAllMocks())

function renderFailure(error: unknown): string {
  vi.spyOn(console, 'error').mockImplementation(() => {})
  return renderToStaticMarkup(
    <QueryBoundary query={{ data: undefined, isPending: false, error, refetch: () => {} }}>
      {() => <p>rows</p>}
    </QueryBoundary>,
  ).replace(/<[^>]*>/g, ' ')
}

describe('QueryBoundary', () => {
  it('says a 404 is not found rather than "is not served yet"', () => {
    const markup = renderFailure(new ApiError(404, '/reports/cashflow', null))
    expect(markup).toContain('Not found.')
    expect(markup).not.toContain('/reports/cashflow')
    expect(markup).not.toContain('not served')
  })

  it('says a 500 is a server problem rather than quoting the status', () => {
    const markup = renderFailure(new ApiError(500, '/reports/cashflow', null))
    expect(markup).toContain('The server had a problem')
    expect(markup).not.toContain('500')
    expect(markup).not.toContain('answered')
  })

  it('offers the request id when the server sent one', () => {
    expect(renderFailure(new ApiError(503, '/dashboard', null, 'req-77'))).toContain('req-77')
  })

  it('logs the status and path to the console instead of to the screen', () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
    renderToStaticMarkup(
      <QueryBoundary
        query={{
          data: undefined,
          isPending: false,
          error: new ApiError(500, '/reports/cashflow', null),
          refetch: () => {},
        }}
      >
        {() => <p>rows</p>}
      </QueryBoundary>,
    )
    expect(spy.mock.calls[0][0]).toContain('/reports/cashflow')
  })

  it('names a network failure as unreachable', () => {
    expect(renderFailure(new TypeError('Failed to fetch'))).toContain('reach the server')
  })

  it('still renders children when the query succeeded', () => {
    expect(
      renderToStaticMarkup(
        <QueryBoundary query={{ data: 3, isPending: false, error: null, refetch: () => {} }}>
          {(value) => <p>{value}</p>}
        </QueryBoundary>,
      ),
    ).toContain('3')
  })
})

describe('LoadFailure', () => {
  it('names what failed to load and offers a retry', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    const markup = renderToStaticMarkup(
      <LoadFailure
        query={{ error: new ApiError(500, '/goals', null), refetch: () => {} }}
        title="Could not load savings goals"
      />,
    )
    expect(markup).toContain('Could not load savings goals')
    expect(markup).toContain('Try again')
    expect(markup).not.toContain('/goals')
  })
})
