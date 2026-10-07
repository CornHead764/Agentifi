import type { Page, Route } from '@playwright/test'

import { FIXTURES, type Fixture } from './fixtures'
import { resetState } from './fixtures/state'

/** A fixture's answer when it is more than a 200 with a JSON body. */
export interface Answered {
  status: number
  body: unknown
  /** Holds the answer back, so a request in flight is seen on screen. */
  delayMs?: number
  /** Served as-is with this content type instead of as JSON. */
  binary?: { contentType: string; bytes: Buffer }
}

export type Handler = (query: URLSearchParams, body: unknown, path: string) => unknown

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

function requestBody(route: Route): unknown {
  try {
    return route.request().postDataJSON()
  } catch {
    // A multipart upload: the handler reads its fields out of the raw text.
    return route.request().postData()
  }
}

/**
 * Serve `/api/**` from the demo household. A GET with no fixture answers 404
 * and is recorded in `missing`; a write with no fixture answers `{}`. Every
 * call starts the household afresh, so a scene's clicks never leak into the next.
 */
export async function serveFixtures(page: Page, missing: string[]): Promise<void> {
  resetState()
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
      const value = isHandler(fixture) ? fixture(url.searchParams, requestBody(route), path) : fixture
      answer = isAnswered(value) ? value : { status: 200, body: value }
    }
    if (answer.delayMs) await new Promise((done) => setTimeout(done, answer.delayMs))
    if (answer.binary) {
      await route.fulfill({ status: answer.status, contentType: answer.binary.contentType, body: answer.binary.bytes })
      return
    }
    await route.fulfill({
      status: answer.status,
      contentType: 'application/json',
      body: JSON.stringify(answer.body),
    })
  })
}

function isHandler(value: unknown): value is Handler {
  return typeof value === 'function'
}

function isAnswered(value: unknown): value is Answered {
  if (typeof value !== 'object' || value === null || !('status' in value) || !('body' in value)) return false
  return Object.keys(value).every((key) => ['status', 'body', 'delayMs', 'binary'].includes(key))
}
