/**
 * The authenticator setup key, checked before it is sent. The mistake worth
 * catching is a six-digit code pasted where the key goes: it would be sealed
 * and only fail on a later unattended sign-in.
 */

const BASE32 = /^[A-Z2-7]+=*$/

/** A setup key as a provider prints it — spaced, lower case — in one spelling. */
export function normalizeAuthKey(value: string): string {
  return value.replace(/[\s-]/g, '').toUpperCase()
}

/** What is wrong with this key, in the household's words, or null for nothing. */
export function authKeyProblem(value: string): string | null {
  const key = normalizeAuthKey(value)
  if (key === '') return null
  if (/^\d+$/.test(key)) {
    return 'That looks like a code from the app, not the setup key it was added with.'
  }
  if (!BASE32.test(key)) {
    return 'A setup key is letters A–Z and digits 2–7. Copy it from the provider, spaces and all.'
  }
  return null
}
