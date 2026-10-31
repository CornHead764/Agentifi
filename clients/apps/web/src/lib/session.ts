/**
 * The bearer token, outside React so the API client can read it synchronously
 * and a 401 anywhere can clear the session. localStorage rather than a cookie:
 * the API is bearer-authenticated, and a cookie would need CSRF protection.
 */

import { createListenerSet } from './listenerSet'
import { clearStored, onStoredChange, readStored, writeStored } from './storage'

const TOKEN_KEY = 'accessToken'

export const authKeys = {
  me: ['auth', 'me'] as const,
  meFor: (token: string | null) => ['auth', 'me', token] as const,
}

const listeners = createListenerSet<[string | null]>()

let token: string | null = readStored(TOKEN_KEY)

/** Read on every request, never cached upstream. */
export function accessToken(): string | null {
  return token
}

export function setAccessToken(next: string | null): void {
  if (token === next) return
  token = next
  if (next === null) clearStored(TOKEN_KEY)
  else writeStored(TOKEN_KEY, next)
  listeners.notify(next)
}

export function clearAccessToken(): void {
  setAccessToken(null)
}

/** Subscribe to sign-in and sign-out, including from another tab (via the `storage` event). */
export function onAccessTokenChange(listener: (token: string | null) => void): () => void {
  const unsubscribe = listeners.subscribe(listener)
  const stopListening = onStoredChange(TOKEN_KEY, () => {
    token = readStored(TOKEN_KEY)
    listener(token)
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
