/**
 * Whether this browser is signed in. The session itself is an HttpOnly cookie
 * the page cannot read; this is a marker beside it, kept outside React so the
 * API client can read it synchronously and a 401 anywhere can clear it. It
 * holds no credential.
 */

import { createListenerSet } from './listenerSet'
import { clearStored, onStoredChange, readStored, writeStored } from './storage'

const SESSION_KEY = 'session'

export const authKeys = {
  me: ['auth', 'me'] as const,
  meFor: (session: string | null) => ['auth', 'me', session] as const,
}

const listeners = createListenerSet<[string | null]>()

let session: string | null = readStored(SESSION_KEY)

/**
 * A name for this sign-in, or null when signed out. A new sign-in gets a new
 * one, so the next person is never served the last one's cache.
 */
export function currentSession(): string | null {
  return session
}

function setSession(next: string | null): void {
  if (session === next) return
  session = next
  if (next === null) clearStored(SESSION_KEY)
  else writeStored(SESSION_KEY, next)
  listeners.notify(next)
}

/** The server has just set the session cookie. */
export function markSignedIn(): void {
  setSession(`${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`)
}

export function markSignedOut(): void {
  setSession(null)
}

/** Subscribe to sign-in and sign-out, including from another tab (via the `storage` event). */
export function onSessionChange(listener: (session: string | null) => void): () => void {
  const unsubscribe = listeners.subscribe(listener)
  const stopListening = onStoredChange(SESSION_KEY, () => {
    session = readStored(SESSION_KEY)
    listener(session)
  })
  return () => {
    unsubscribe()
    stopListening()
  }
}

const LAST_EMAIL_KEY = 'lastEmail'

export function rememberEmail(email: string): void {
  writeStored(LAST_EMAIL_KEY, email)
}

export function lastEmail(): string {
  return readStored(LAST_EMAIL_KEY) ?? ''
}
