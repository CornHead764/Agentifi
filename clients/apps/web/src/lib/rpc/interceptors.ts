/**
 * The session on every call: the space it is about, and what a refusal of the
 * session or the space means. The session itself travels as a cookie.
 */

import { Code, ConnectError, type Interceptor } from '@connectrpc/connect'

export const SPACE_HEADER = 'X-Space-Id'

/** Read per call, so a call after a sign-in or a space switch carries the new values. */
export interface Session {
  /** Whether this client is signed in; the session cookie itself is out of a script's reach. */
  signedIn(): boolean
  /** Null for whichever space the server defaults to. */
  space(): string | null
  /** The session was refused: forget it. */
  signedOut(): void
  /** The space asked for is gone or was never the caller's: stop asking for it. */
  spaceGone(): void
}

export function sessionHeaders(session: Session): Interceptor {
  return (next) => async (request) => {
    const space = session.space()
    if (space) request.header.set(SPACE_HEADER, space)
    return next(request)
  }
}

/** Only a call sent signed in can sign out, and only one that named a space can drop it. */
export function sessionFailures(session: Session): Interceptor {
  return (next) => async (request) => {
    const signedIn = session.signedIn()
    try {
      return await next(request)
    } catch (error) {
      if (error instanceof ConnectError) {
        if (error.code === Code.Unauthenticated && signedIn) {
          session.signedOut()
        }
        if (
          error.code === Code.NotFound &&
          request.header.has(SPACE_HEADER) &&
          error.rawMessage === 'Space not found'
        ) {
          session.spaceGone()
        }
      }
      throw error
    }
  }
}
