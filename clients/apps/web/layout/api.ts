import type { Page, Route } from '@playwright/test'

import { FIXTURES, type Fixture } from './fixtures'

export interface Answered {
  status: number
  body: unknown
}

/** A fixture's key is `METHOD /path`, with `:name` for a segment that varies. */
function matches(pattern: string, path: string): boolean {
  const want = pattern.split('/')
  const have = path.split('/')
  if (want.length !== have.length) return false
  return want.every((segment, index) => segment.startsWith(':') || segment === have[index])
}

function lookup(method: string, path: string): Fixture | undefined {
  const exact = FIXTURES[`${method} ${path}`]
  if (exact !== undefined) return exact
  const key = Object.keys(FIXTURES).find((candidate) => {
    const [verb, pattern] = candidate.split(' ')
    return verb === method && matches(pattern, path)
  })
  return key === undefined ? undefined : FIXTURES[key]
}

/**
 * Serve `/api/**` from the fixtures. A GET with no fixture answers 404 and is
 * recorded in `missing`, which fails the page: a screen drawn around a failed
 * request is not the screen being checked. A write answers `{}`.
 */
export async function serveFixtures(page: Page, missing: string[]): Promise<void> {
  await page.route('**/api/**', async (route: Route) => {
    const url = new URL(route.request().url())
    const method = route.request().method()
    const path = url.pathname.replace(/^\/api/, '')
    const fixture = lookup(method, path)

    let answer: Answered
    if (fixture === undefined) {
      if (method === 'GET') missing.push(`GET ${path}`)
      answer = method === 'GET' ? { status: 404, body: { detail: 'No fixture' } } : { status: 200, body: {} }
    } else {
      const value = isHandler(fixture) ? fixture(url.searchParams, route.request().postDataJSON()) : fixture
      answer = isAnswered(value) ? value : { status: 200, body: value }
    }
    await route.fulfill({
      status: answer.status,
      contentType: 'application/json',
      body: JSON.stringify(answer.body),
    })
  })
}

function isHandler(value: unknown): value is (query: URLSearchParams, body: unknown) => unknown {
  return typeof value === 'function'
}

function isAnswered(value: unknown): value is Answered {
  return (
    typeof value === 'object' &&
    value !== null &&
    'status' in value &&
    'body' in value &&
    Object.keys(value).length === 2
  )
}
