import { describe, expect, it } from 'vitest'

import { ApiError } from './api'
import { isPasskeyPromptCancelled, passkeyMessageFor } from './passkeyErrors'

describe('isPasskeyPromptCancelled', () => {
  it('recognizes the error the browser gives for a closed prompt', () => {
    expect(isPasskeyPromptCancelled(new DOMException('', 'NotAllowedError'))).toBe(true)
  })

  it('does not mistake an unrelated failure for a cancelled prompt', () => {
    expect(isPasskeyPromptCancelled(new DOMException('', 'InvalidStateError'))).toBe(false)
    expect(isPasskeyPromptCancelled(new Error('boom'))).toBe(false)
    expect(isPasskeyPromptCancelled(new ApiError(500, '/auth/passkeys/authenticate/verify', null))).toBe(
      false,
    )
  })
})

describe('passkeyMessageFor', () => {
  it('says a passkey was not recognized rather than repeating the password wording', () => {
    expect(passkeyMessageFor(new ApiError(401, '/x', null))).toBe('That passkey is not recognized.')
  })

  it('reads the server detail when there is one', () => {
    expect(passkeyMessageFor(new ApiError(400, '/x', { detail: 'Bad request' }))).toBe('Bad request')
  })

  it('names the rate limit the same way the password flow does', () => {
    expect(passkeyMessageFor(new ApiError(429, '/x', null))).toBe(
      'Too many attempts. Wait a minute and try again.',
    )
  })

  it('falls back for anything that is not the server refusing', () => {
    expect(passkeyMessageFor(new Error('network down'))).toBe('Could not use a passkey to sign in.')
  })
})
