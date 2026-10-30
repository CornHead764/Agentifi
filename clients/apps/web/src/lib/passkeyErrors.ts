import { ApiError } from '@/lib/api'

/** WebAuthn reports a cancelled prompt and "no eligible passkey" alike, as `NotAllowedError`. */
export function isPasskeyPromptCancelled(failure: unknown): boolean {
  return failure instanceof DOMException && failure.name === 'NotAllowedError'
}

/**
 * What a sign-in failure says once the caller has handled its own 401: the
 * rate limit and the server's own detail read the same whether the attempt
 * was a password or a passkey.
 */
export function signInFailureDetail(failure: ApiError): string {
  if (failure.status === 429) return 'Too many attempts. Wait a minute and try again.'
  return failure.detail ?? 'Something went wrong signing in.'
}

export function passkeyMessageFor(failure: unknown): string {
  if (failure instanceof ApiError) {
    if (failure.status === 401) return 'That passkey is not recognized.'
    return signInFailureDetail(failure)
  }
  return 'Could not use a passkey to sign in.'
}
