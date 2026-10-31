import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  creationOptionsFromServer,
  isSecureContext,
  requestOptionsFromServer,
} from './webauthn'

// The base64url the server sends must become exactly the bytes it encodes.

describe('creationOptionsFromServer', () => {
  it('turns the challenge and user id back into the bytes they encode', () => {
    // "hello" and "world", base64url with no padding.
    const options = creationOptionsFromServer({
      publicKey: {
        challenge: 'aGVsbG8',
        rp: { name: 'Agentifi' },
        user: { id: 'd29ybGQ', name: 'ada@example.test', displayName: 'Ada' },
        pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
      },
    })

    expect(bytesOf(options.publicKey!.challenge as ArrayBuffer)).toEqual(bytesOf(textBuffer('hello')))
    expect(bytesOf(options.publicKey!.user!.id as ArrayBuffer)).toEqual(bytesOf(textBuffer('world')))
  })

  it('decodes an excluded credential id the same way, so a re-enrolled key is actually refused', () => {
    const options = creationOptionsFromServer({
      publicKey: {
        challenge: 'aGVsbG8',
        rp: { name: 'Agentifi' },
        user: { id: 'd29ybGQ', name: 'a', displayName: 'a' },
        pubKeyCredParams: [],
        excludeCredentials: [{ id: 'aGVsbG8', type: 'public-key' }],
      },
    })

    expect(bytesOf(options.publicKey!.excludeCredentials![0].id as ArrayBuffer)).toEqual(
      bytesOf(textBuffer('hello')),
    )
  })

  it('carries fields it does not touch straight through', () => {
    const options = creationOptionsFromServer({
      publicKey: {
        challenge: 'aGVsbG8',
        rp: { name: 'Agentifi', id: 'agentifi.example' },
        user: { id: 'd29ybGQ', name: 'a', displayName: 'a' },
        pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
        attestation: 'none',
      },
    })

    expect(options.publicKey!.rp).toEqual({ name: 'Agentifi', id: 'agentifi.example' })
    expect(options.publicKey!.attestation).toBe('none')
  })
})

describe('requestOptionsFromServer', () => {
  it('turns the challenge back into bytes with no allow list, for a discoverable login', () => {
    const options = requestOptionsFromServer({
      publicKey: { challenge: 'aGVsbG8' },
    })

    expect(bytesOf(options.publicKey!.challenge as ArrayBuffer)).toEqual(bytesOf(textBuffer('hello')))
    expect(options.publicKey!.allowCredentials).toEqual([])
  })
})

describe('isSecureContext', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('is false with no window to ask, rather than throwing', () => {
    expect(isSecureContext()).toBe(false)
  })

  it('reports what window says once there is one', () => {
    vi.stubGlobal('window', { isSecureContext: true })
    expect(isSecureContext()).toBe(true)

    vi.stubGlobal('window', { isSecureContext: false })
    expect(isSecureContext()).toBe(false)
  })
})

function textBuffer(text: string): ArrayBuffer {
  return Uint8Array.from(text, (char) => char.charCodeAt(0)).buffer
}

function bytesOf(buffer: ArrayBuffer): number[] {
  return Array.from(new Uint8Array(buffer))
}
