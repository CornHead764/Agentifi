/**
 * A Connect refusal as the `ApiError` every screen already reads, so moving a
 * call to a procedure changes nothing about how its failure is shown.
 */

import { Code, ConnectError } from '@connectrpc/connect'

import { ApiError } from '@/lib/apiError'

import { problemOf, statusOf } from './problem'

/** Anything but a ConnectError, and a cancellation, passes through as it was. */
export function toApiError(error: unknown, procedure: string): unknown {
  if (!(error instanceof ConnectError) || error.code === Code.Canceled) return error
  const problem = problemOf(error)
  const body: Record<string, unknown> = { detail: error.rawMessage }
  if (problem?.fields.length) {
    body.detail = problem.fields.map(({ loc, msg, type }) => ({ loc, msg, type }))
  }
  if (problem?.code) body.code = problem.code
  if (problem?.hasFailureScreenshot) body.has_failure_screenshot = true
  return new ApiError(statusOf(error), procedure, body, error.metadata.get('X-Request-Id'))
}
