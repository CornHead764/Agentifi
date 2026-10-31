/**
 * An age X25519 key pair, made in the browser so the private half never
 * reaches the server. Written out here rather than through `crypto.subtle`,
 * which a browser withholds outside a secure context, and an install reached
 * over plain HTTP by its address is not one. Only the randomness comes from
 * the platform (`crypto.getRandomValues`, which every context has).
 *
 * X25519 is RFC 7748; the encoding is Bech32 (BIP-173, not Bech32m), with
 * the human-readable parts age uses: `age` for the public recipient and
 * `age-secret-key-` for the identity, which age writes in upper case.
 */

const P = (1n << 255n) - 19n
const A24 = 121665n

function mod(value: bigint): bigint {
  const r = value % P
  return r < 0n ? r + P : r
}

function power(base: bigint, exponent: bigint): bigint {
  let result = 1n
  let b = mod(base)
  let e = exponent
  while (e > 0n) {
    if (e & 1n) result = mod(result * b)
    b = mod(b * b)
    e >>= 1n
  }
  return result
}

function littleEndian(bytes: Uint8Array): bigint {
  let value = 0n
  for (let i = bytes.length - 1; i >= 0; i--) value = (value << 8n) | BigInt(bytes[i])
  return value
}

function toLittleEndian(value: bigint): Uint8Array {
  const out = new Uint8Array(32)
  let v = value
  for (let i = 0; i < 32; i++) {
    out[i] = Number(v & 0xffn)
    v >>= 8n
  }
  return out
}

/** RFC 7748 section 5: the scalar `k` times the point with u-coordinate `u`. */
export function x25519(scalar: Uint8Array, u: Uint8Array): Uint8Array {
  if (scalar.length !== 32 || u.length !== 32) throw new Error('X25519 takes 32-byte inputs')
  const clamped = Uint8Array.from(scalar)
  clamped[0] &= 248
  clamped[31] &= 127
  clamped[31] |= 64
  const k = littleEndian(clamped)
  const masked = Uint8Array.from(u)
  masked[31] &= 127
  const x1 = mod(littleEndian(masked))

  let x2 = 1n
  let z2 = 0n
  let x3 = x1
  let z3 = 1n
  let swap = 0n
  for (let t = 254n; t >= 0n; t--) {
    const bit = (k >> t) & 1n
    swap ^= bit
    if (swap) {
      ;[x2, x3] = [x3, x2]
      ;[z2, z3] = [z3, z2]
    }
    swap = bit

    const a = mod(x2 + z2)
    const aa = mod(a * a)
    const b = mod(x2 - z2)
    const bb = mod(b * b)
    const e = mod(aa - bb)
    const c = mod(x3 + z3)
    const d = mod(x3 - z3)
    const da = mod(d * a)
    const cb = mod(c * b)
    x3 = mod((da + cb) * (da + cb))
    z3 = mod(x1 * mod((da - cb) * (da - cb)))
    x2 = mod(aa * bb)
    z2 = mod(e * (aa + A24 * e))
  }
  if (swap) {
    ;[x2, x3] = [x3, x2]
    ;[z2, z3] = [z3, z2]
  }
  return toLittleEndian(mod(x2 * power(z2, P - 2n)))
}

const BASE_POINT = toLittleEndian(9n)

const CHARSET = 'qpzry9x8gf2tvdw0s3jn54khce6mua7l'
const GENERATOR = [0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3]

function polymod(values: number[]): number {
  let chk = 1
  for (const value of values) {
    const top = chk >>> 25
    chk = ((chk & 0x1ffffff) << 5) ^ value
    for (let i = 0; i < 5; i++) {
      if ((top >>> i) & 1) chk ^= GENERATOR[i]
    }
  }
  return chk >>> 0
}

function hrpExpand(hrp: string): number[] {
  const out: number[] = []
  for (const char of hrp) out.push(char.charCodeAt(0) >> 5)
  out.push(0)
  for (const char of hrp) out.push(char.charCodeAt(0) & 31)
  return out
}

/** Bech32 of five-bit words, in lower case. */
export function bech32Encode(hrp: string, words: number[]): string {
  const values = [...hrpExpand(hrp), ...words, 0, 0, 0, 0, 0, 0]
  const mod32 = polymod(values) ^ 1
  const checksum = Array.from({ length: 6 }, (_, i) => (mod32 >>> (5 * (5 - i))) & 31)
  return `${hrp}1${[...words, ...checksum].map((word) => CHARSET[word]).join('')}`
}

/** The human-readable part and five-bit words of a Bech32 string; throws on a bad checksum. */
export function bech32Decode(text: string): { hrp: string; words: number[] } {
  if (text !== text.toLowerCase() && text !== text.toUpperCase()) {
    throw new Error('Bech32 is all upper or all lower case')
  }
  const lower = text.toLowerCase()
  const separator = lower.lastIndexOf('1')
  if (separator < 1 || separator + 7 > lower.length) throw new Error('not a Bech32 string')
  const hrp = lower.slice(0, separator)
  const data: number[] = []
  for (const char of lower.slice(separator + 1)) {
    const word = CHARSET.indexOf(char)
    if (word < 0) throw new Error(`"${char}" is not a Bech32 character`)
    data.push(word)
  }
  if (polymod([...hrpExpand(hrp), ...data]) !== 1) throw new Error('the Bech32 checksum does not match')
  return { hrp, words: data.slice(0, -6) }
}

function toWords(bytes: Uint8Array): number[] {
  const out: number[] = []
  let accumulator = 0
  let bits = 0
  for (const byte of bytes) {
    accumulator = (accumulator << 8) | byte
    bits += 8
    while (bits >= 5) {
      bits -= 5
      out.push((accumulator >> bits) & 31)
    }
  }
  if (bits > 0) out.push((accumulator << (5 - bits)) & 31)
  return out
}

function fromWords(words: number[]): Uint8Array {
  const out: number[] = []
  let accumulator = 0
  let bits = 0
  for (const word of words) {
    accumulator = (accumulator << 5) | word
    bits += 5
    if (bits >= 8) {
      bits -= 8
      out.push((accumulator >> bits) & 0xff)
    }
  }
  if (bits >= 5 || ((accumulator << (8 - bits)) & 0xff) !== 0) {
    throw new Error('the Bech32 data has stray padding')
  }
  return Uint8Array.from(out)
}

const RECIPIENT_HRP = 'age'
const IDENTITY_HRP = 'age-secret-key-'

export interface AgeKeyPair {
  /** The private half, `AGE-SECRET-KEY-1…`. Never sent anywhere. */
  identity: string
  /** The public half, `age1…`, which is all the server is given. */
  recipient: string
}

function recipientOf(scalar: Uint8Array): string {
  return bech32Encode(RECIPIENT_HRP, toWords(x25519(scalar, BASE_POINT)))
}

/** The public recipient an identity encrypts to. */
export function recipientFromIdentity(identity: string): string {
  const { hrp, words } = bech32Decode(identity.trim())
  if (hrp !== IDENTITY_HRP) throw new Error('not an age identity (AGE-SECRET-KEY-1…)')
  const scalar = fromWords(words)
  if (scalar.length !== 32) throw new Error('an age identity holds 32 bytes')
  return recipientOf(scalar)
}

/** Whether `text` is an age X25519 recipient, `age1…` over 32 bytes. */
export function isAgeRecipient(text: string): boolean {
  try {
    const { hrp, words } = bech32Decode(text.trim())
    return hrp === RECIPIENT_HRP && fromWords(words).length === 32 && text.trim() === text.trim().toLowerCase()
  } catch {
    return false
  }
}

export function generateAgeKeyPair(): AgeKeyPair {
  const scalar = new Uint8Array(32)
  crypto.getRandomValues(scalar)
  return {
    identity: bech32Encode(IDENTITY_HRP, toWords(scalar)).toUpperCase(),
    recipient: recipientOf(scalar),
  }
}

/** The identity file as age-keygen writes it, which `age -d -i` reads. */
export function identityFile(pair: AgeKeyPair, createdAt: Date): string {
  return `# created: ${createdAt.toISOString()}\n# public key: ${pair.recipient}\n${pair.identity}\n`
}
