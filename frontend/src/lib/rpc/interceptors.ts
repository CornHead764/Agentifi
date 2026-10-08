/**
 * The session on every call: the bearer token and the space it is about, and
 * what a refusal of either means for the session.
 */

import { Code, ConnectError, type Interceptor } from '@connectrpc/connect'

import { problemOf } from './problem'

export const SPACE_HEADER = 'X-Space-Id'

/** Read per call, so a call after a sign-in or a space switch carries the new values. */
export interface Session {
  token(): string | null
  /** Null for whichever space the server defaults to. */
  space(): string | null
  /** The token was refused: forget it. */
  signedOut(): void
  /** The space asked for is gone or was never the caller's: stop asking for it. */
  spaceGone(): void
}

export function sessionHeaders(session: Session): Interceptor {
  return (next) => async (request) => {
    const token = session.token()
    const space = session.space()
    if (token) request.header.set('Authorization', `Bearer ${token}`)
    if (space) request.header.set(SPACE_HEADER, space)
    return next(request)
  }
}

/** Only a call that sent a token can sign out, and only one that named a space can drop it. */
export function sessionFailures(session: Session): Interceptor {
  return (next) => async (request) => {
    try {
      return await next(request)
    } catch (error) {
      if (error instanceof ConnectError) {
        if (error.code === Code.Unauthenticated && request.header.has('Authorization')) {
          session.signedOut()
        }
        if (
          error.code === Code.NotFound &&
          request.header.has(SPACE_HEADER) &&
          problemOf(error)?.code === 'space_not_found'
        ) {
          session.spaceGone()
        }
      }
      throw error
    }
  }
}
