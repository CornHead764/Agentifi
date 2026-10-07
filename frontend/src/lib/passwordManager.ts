/**
 * Handing a secret made on this screen to the browser's password manager, or
 * an extension's such as Bitwarden, and having it filled back in later.
 *
 * Two ways, since neither reaches every manager. A real form with a
 * `username` and a `new-password` field, submitted and then taken off the
 * page, is what browsers and extensions read as a sign-up and offer to save.
 * The Credential Management API (`navigator.credentials.store` with a
 * `PasswordCredential`) asks the browser's own manager directly, but only
 * Chromium has `PasswordCredential`, and only in a secure context, which an
 * install reached over plain HTTP by its address is not.
 */

/** The login a backup key is saved under, the same when saved and when filled. */
export function backupKeyUsername(host: string): string {
  return `agentifi-backup@${host}`
}

/** The parts of `window` the Credential Management path reads. */
export interface CredentialContext {
  isSecureContext: boolean
  /** Chromium's constructor, missing from other browsers and from TypeScript's DOM types. */
  PasswordCredential?: unknown
  navigator: { credentials?: { store?: (credential: Credential) => Promise<unknown> } }
}

/**
 * Asks the browser's password manager to store the secret. Resolves false
 * where it cannot be asked, or the asking failed; the submitted form is the
 * other way and does not depend on this.
 */
export async function storePasswordCredential(
  context: CredentialContext,
  username: string,
  secret: string,
  name: string,
): Promise<boolean> {
  const Constructor = context.PasswordCredential
  const credentials = context.navigator.credentials
  if (!context.isSecureContext || typeof Constructor !== 'function' || typeof credentials?.store !== 'function') {
    return false
  }
  try {
    const credential: Credential = Reflect.construct(Constructor, [{ id: username, password: secret, name }])
    await credentials.store(credential)
    return true
  } catch {
    return false
  }
}
