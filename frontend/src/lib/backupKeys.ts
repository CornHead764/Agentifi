/**
 * The keys a backup set can be encrypted to and opened with, as the server
 * takes them (backend/internal/backup/crypt.go): an age X25519 recipient, or
 * an SSH public key of the two types age encrypts to, `ssh-ed25519` and
 * `ssh-rsa` of at least 2048 bits. Read here so a pasted key is named and
 * refused before it is sent; the server checks it again.
 */

import { isAgeRecipient } from './ageKeys'

export type RecipientKind = 'age' | 'ssh-ed25519' | 'ssh-rsa'

export interface RecipientDescription {
  kind: RecipientKind
  /** An SSH key's trailing comment, often user@host; empty when it has none. */
  comment: string
}

/** What a recipient is called on screen. */
export const RECIPIENT_KIND_LABELS: Record<string, string> = {
  age: 'age',
  'ssh-ed25519': 'SSH Ed25519',
  'ssh-rsa': 'SSH RSA',
}

/** RSA keys age refuses to encrypt to. */
const MIN_RSA_BITS = 2048

function decodeBase64(text: string): Uint8Array | null {
  try {
    return Uint8Array.from(atob(text), (char) => char.charCodeAt(0))
  } catch {
    return null
  }
}

/** The length-prefixed strings of the SSH wire format (RFC 4251 section 5). */
function wireStrings(bytes: Uint8Array): Uint8Array[] | null {
  const out: Uint8Array[] = []
  let at = 0
  while (at < bytes.length) {
    if (at + 4 > bytes.length) return null
    const length = ((bytes[at] << 24) | (bytes[at + 1] << 16) | (bytes[at + 2] << 8) | bytes[at + 3]) >>> 0
    at += 4
    if (at + length > bytes.length) return null
    out.push(bytes.subarray(at, at + length))
    at += length
  }
  return out
}

/** The bit length of an unsigned big-endian integer (an mpint's magnitude). */
function bitLength(value: Uint8Array): number {
  let i = 0
  while (i < value.length && value[i] === 0) i++
  if (i === value.length) return 0
  return (value.length - i - 1) * 8 + (32 - Math.clz32(value[i]))
}

function describeSSH(text: string): RecipientDescription | null {
  const [type, encoded, ...comment] = text.split(/\s+/)
  if (type !== 'ssh-ed25519' && type !== 'ssh-rsa') return null
  const blob = encoded ? decodeBase64(encoded) : null
  const fields = blob ? wireStrings(blob) : null
  if (!fields || new TextDecoder().decode(fields[0]) !== type) return null
  if (type === 'ssh-ed25519' && (fields.length !== 2 || fields[1].length !== 32)) return null
  if (type === 'ssh-rsa' && (fields.length !== 3 || bitLength(fields[2]) < MIN_RSA_BITS)) return null
  return { kind: type, comment: comment.join(' ') }
}

/** A recipient the server will take, named; null for anything else. */
export function describeRecipient(text: string): RecipientDescription | null {
  const trimmed = text.trim()
  if (isAgeRecipient(trimmed)) return { kind: 'age', comment: '' }
  return describeSSH(trimmed)
}

/** Whether an identity is an SSH private key, which may need a passphrase. */
export function isSSHPrivateKey(identity: string): boolean {
  return /-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----/.test(identity)
}
