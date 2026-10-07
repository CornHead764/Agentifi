import { describe, expect, it } from 'vitest'

import {
  bech32Decode,
  bech32Encode,
  generateAgeKeyPair,
  identityFile,
  isAgeRecipient,
  recipientFromIdentity,
  x25519,
} from './ageKeys'

function hex(text: string): Uint8Array {
  return Uint8Array.from(text.match(/../g)!.map((pair) => parseInt(pair, 16)))
}

function toHex(bytes: Uint8Array): string {
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')
}

const BASE = hex('0900000000000000000000000000000000000000000000000000000000000000')

describe('X25519', () => {
  // RFC 7748, section 5.2.
  it('matches the RFC test vectors', () => {
    expect(
      toHex(
        x25519(
          hex('a546e36bf0527c9d3b16154b82465edd62144c0ac1fc5a18506a2244ba449ac4'),
          hex('e6db6867583030db3594c1a424b15f7c726624ec26b3353b10a903a6d0ab1c4c'),
        ),
      ),
    ).toBe('c3da55379de9c6908e94ea4df28d084f32eccf03491c71f754b4075577a28552')
    expect(
      toHex(
        x25519(
          hex('4b66e9d4d1b4673c5ad22691957d6af5c11b6421e0ea01d42ca4169e7918ba0d'),
          hex('e5210f12786811d3f4b7959d0538ae2c31dbe7106fc03c3efc4cd549c715a493'),
        ),
      ),
    ).toBe('95cbde9476e8907d7aade45cb4b873f88b595a68799fa152e6f8f7647aac7957')
  })

  // RFC 7748, section 6.1: Alice's and Bob's public keys.
  it('derives the RFC Diffie-Hellman public keys', () => {
    expect(toHex(x25519(hex('77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a'), BASE))).toBe(
      '8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a',
    )
    expect(toHex(x25519(hex('5dab087e624a8a4b79e17f8b83800ee66f3bb1292618b6fd1c2f8b27ff88e0eb'), BASE))).toBe(
      'de9edb7d7b7dc1b4d35b61c2ece435373f8343c85b78674dadfc7e146f882b4f',
    )
  })
})

describe('Bech32', () => {
  // BIP-173's valid test vectors.
  it('reads and writes the BIP-173 examples', () => {
    expect(bech32Decode('A12UEL5L')).toEqual({ hrp: 'a', words: [] })
    const every = 'abcdef1qpzry9x8gf2tvdw0s3jn54khce6mua7lmqqqxw'
    const decoded = bech32Decode(every)
    expect(decoded.hrp).toBe('abcdef')
    expect(decoded.words).toEqual(Array.from({ length: 32 }, (_, i) => i))
    expect(bech32Encode(decoded.hrp, decoded.words)).toBe(every)
  })

  it('refuses a broken checksum and mixed case', () => {
    expect(() => bech32Decode('abcdef1qpzry9x8gf2tvdw0s3jn54khce6mua7lmqqqxx')).toThrow()
    expect(() => bech32Decode('A12uEL5L')).toThrow()
  })
})

describe('an age key pair', () => {
  // A throwaway pair the Go age library (filippo.io/age) generated for these tests.
  it('derives the recipient age itself derives', () => {
    expect(recipientFromIdentity('AGE-SECRET-KEY-15WVA2EEQZFV75G9LJENM2PVQS9M3WURETAGJXX4PEMUVLWW40V7SXLFC3W')).toBe(
      'age1n946z2m9xyhkwem4vn9yj7hj7trelehnwv6q09vqqkzfq4sz3v8qp6rp0m',
    )
  })

  it('generates a pair whose halves belong together', () => {
    const pair = generateAgeKeyPair()
    expect(pair.identity).toMatch(/^AGE-SECRET-KEY-1[0-9A-Z]{58}$/)
    expect(pair.recipient).toMatch(/^age1[0-9a-z]{58}$/)
    expect(recipientFromIdentity(pair.identity)).toBe(pair.recipient)
    expect(isAgeRecipient(pair.recipient)).toBe(true)
  })

  it('tells a recipient from an identity or a typo', () => {
    expect(isAgeRecipient('age1n946z2m9xyhkwem4vn9yj7hj7trelehnwv6q09vqqkzfq4sz3v8qp6rp0m')).toBe(true)
    expect(isAgeRecipient('age1n946z2m9xyhkwem4vn9yj7hj7trelehnwv6q09vqqkzfq4sz3v8qp6rp0n')).toBe(false)
    expect(isAgeRecipient('AGE-SECRET-KEY-15WVA2EEQZFV75G9LJENM2PVQS9M3WURETAGJXX4PEMUVLWW40V7SXLFC3W')).toBe(false)
  })

  it('writes the identity file age-keygen writes', () => {
    const file = identityFile(
      { identity: 'AGE-SECRET-KEY-1EXAMPLE', recipient: 'age1example' },
      new Date('2026-01-02T03:04:05Z'),
    )
    expect(file).toBe(
      '# created: 2026-01-02T03:04:05.000Z\n# public key: age1example\nAGE-SECRET-KEY-1EXAMPLE\n',
    )
  })
})
