/**
 * The one place a failure becomes a sentence a user can read; status, path and
 * request id go to the console. A 4xx `detail` is shown; a 5xx body is only
 * logged, since it is whatever leaked out of a handler.
 */

import { ApiError } from '@/lib/api'
import { capitalize } from '@/lib/format'

/** Whether the failed thing was a read or a write; only changes the fallback sentence. */
export type FailureContext = 'load' | 'save'

const logged = new WeakSet<object>()

function logOnce(error: ApiError): void {
  if (logged.has(error)) return
  logged.add(error)
  const reference = error.requestId === null ? '' : ` request ${error.requestId}`
  console.error(`[api] ${error.status} ${error.path}${reference}`, error.body)
}

/**
 * `fetch` rejects with a `TypeError` (browser-dependent message) when it never
 * reached the server. An `AbortError` is not a failure: react-query aborts on refetch.
 */
function isNetworkFailure(error: unknown): boolean {
  if (!(error instanceof Error)) return false
  if (error.name === 'AbortError') return false
  return error instanceof TypeError || /failed to fetch|network/i.test(error.message)
}

function fallback(status: number, context: FailureContext): string {
  if (status === 401 || status === 403) return "You don't have access to this."
  if (status === 404) return 'Not found.'
  if (status === 409) return 'This was changed somewhere else. Reload and try again.'
  if (status === 422) return 'The server refused that change.'
  if (status === 429) return 'Too many requests. Wait a moment and try again.'
  if (status >= 500) return 'The server had a problem. Try again in a moment.'
  return context === 'load' ? 'Could not load this. Try again.' : 'Something went wrong saving this.'
}

/** The request id is appended only to the generic sentences, not to the server's own. */
export function describeApiError(error: unknown, context: FailureContext = 'save'): string {
  if (error instanceof ApiError) {
    logOnce(error)
    const detail = error.status < 500 ? readDetail(error.body) : null
    if (detail !== null) return detail
    const sentence = fallback(error.status, context)
    return error.requestId === null ? sentence : `${sentence} (reference ${error.requestId})`
  }
  if (isNetworkFailure(error)) {
    return "Can't reach the server. Check your connection and try again."
  }
  if (error instanceof Error && error.message !== '') return error.message
  return context === 'load' ? 'Could not load this. Try again.' : 'Something went wrong.'
}

/** `detail` is a string, or a validation list of `{loc, msg}` for refused fields. */
function readDetail(body: unknown): string | null {
  if (typeof body !== 'object' || body === null || !('detail' in body)) return null
  const detail = Reflect.get(body, 'detail')
  if (typeof detail === 'string') return detail
  if (!Array.isArray(detail)) return null

  const messages: string[] = []
  for (const entry of detail) {
    if (typeof entry !== 'object' || entry === null) continue
    const message = Reflect.get(entry, 'msg')
    const location = Reflect.get(entry, 'loc')
    const field = Array.isArray(location) ? location[location.length - 1] : null
    if (typeof message === 'string') {
      messages.push(typeof field === 'string' ? `${fieldLabel(field)}: ${message}` : message)
    }
  }
  return messages.length > 0 ? messages.join('; ') : null
}

/** A wire field name as a person reads it: `autopay_account_id` is "Autopay account". */
function fieldLabel(field: string): string {
  const words = field.replace(/_id$/, '').replace(/_/g, ' ').trim()
  if (words === '') return field
  return capitalize(words)
}
