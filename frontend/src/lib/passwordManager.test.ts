import { describe, expect, it } from 'vitest'

import { backupKeyUsername, storePasswordCredential, type CredentialContext } from './passwordManager'

function context(overrides: Partial<CredentialContext> = {}) {
  const made: unknown[] = []
  const stored: unknown[] = []
  class FakePasswordCredential {
    constructor(init: unknown) {
      made.push(init)
    }
  }
  const value: CredentialContext = {
    isSecureContext: true,
    PasswordCredential: FakePasswordCredential,
    navigator: {
      credentials: {
        store: (credential) => {
          stored.push(credential)
          return Promise.resolve()
        },
      },
    },
    ...overrides,
  }
  return { value, made, stored }
}

describe('saving a backup key to the password manager', () => {
  it('names the login after this install', () => {
    expect(backupKeyUsername('192.0.2.10:8100')).toBe('agentifi-backup@192.0.2.10:8100')
  })

  it('asks the browser to store it where the Credential Management API is there', async () => {
    const { value, made, stored } = context()
    await expect(
      storePasswordCredential(value, 'agentifi-backup@host', 'AGE-SECRET-KEY-1X', 'Backup key'),
    ).resolves.toBe(true)
    expect(made).toEqual([{ id: 'agentifi-backup@host', password: 'AGE-SECRET-KEY-1X', name: 'Backup key' }])
    expect(stored).toHaveLength(1)
  })

  it('does not ask over plain HTTP, where the browser withholds it', async () => {
    const { value, made, stored } = context({ isSecureContext: false })
    await expect(storePasswordCredential(value, 'u', 's', 'n')).resolves.toBe(false)
    expect(made).toHaveLength(0)
    expect(stored).toHaveLength(0)
  })

  it('does not ask a browser without PasswordCredential or credentials.store', async () => {
    const withoutConstructor = context({ PasswordCredential: undefined })
    await expect(storePasswordCredential(withoutConstructor.value, 'u', 's', 'n')).resolves.toBe(false)
    const withoutStore = context({ navigator: {} })
    await expect(storePasswordCredential(withoutStore.value, 'u', 's', 'n')).resolves.toBe(false)
  })

  it('settles false when the browser refuses', async () => {
    const refusing = context({
      navigator: { credentials: { store: () => Promise.reject(new Error('NotAllowedError')) } },
    })
    await expect(storePasswordCredential(refusing.value, 'u', 's', 'n')).resolves.toBe(false)
  })
})
