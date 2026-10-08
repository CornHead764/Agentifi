/**
 * What every failed request throws, whichever wire it went over: the status,
 * and the body the REST API writes for a refusal (`{"detail": ...}`, a list of
 * field errors on a 422, a `code` beside the detail when a client routes on
 * it). The Connect client builds the same thing from a Problem detail.
 */

import { isRecord } from './typeGuards'

export class ApiError extends Error {
  readonly status: number
  readonly path: string
  readonly body: unknown
  /** From `X-Request-Id`: what a user can read off a failure screen to find the log line. */
  readonly requestId: string | null

  constructor(status: number, path: string, body: unknown, requestId: string | null = null) {
    super(`${status} on ${path}`)
    this.name = 'ApiError'
    this.status = status
    this.path = path
    this.body = body
    this.requestId = requestId
  }

  /** The server's sentence. A 422's detail is a list of field errors; this returns the first. */
  get detail(): string | null {
    if (!isRecord(this.body)) return null
    const detail = this.body.detail
    if (typeof detail === 'string') return detail
    if (Array.isArray(detail)) {
      const first = detail[0]
      if (isRecord(first) && typeof first.msg === 'string') return first.msg
    }
    return null
  }

  /** The machine-readable reason, e.g. `password_change_required`, which routes. */
  get code(): string | null {
    if (!isRecord(this.body)) return null
    return typeof this.body.code === 'string' ? this.body.code : null
  }
}
