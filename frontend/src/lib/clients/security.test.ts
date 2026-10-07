import { describe, expect, it } from 'vitest'

import { passkeyUsageLabel, recoveryCodesLabel, type Passkey } from './security'

describe('recoveryCodesLabel', () => {
  it('pluralizes, except for exactly one', () => {
    expect(recoveryCodesLabel(0)).toBe('0 recovery codes left')
    expect(recoveryCodesLabel(1)).toBe('1 recovery code left')
    expect(recoveryCodesLabel(8)).toBe('8 recovery codes left')
  })
})

describe('passkeyUsageLabel', () => {
  function passkey(overrides: Partial<Passkey> = {}): Passkey {
    return {
      id: 'k1',
      name: 'YubiKey',
      created_at: '2026-01-02T09:00:00Z',
      last_used_at: null,
      transports: [],
      rp_id: null,
      ...overrides,
    }
  }

  it('says a passkey has never been used to sign in', () => {
    expect(passkeyUsageLabel(passkey())).toContain('never used')
  })

  it('names the day it last signed somebody in', () => {
    const label = passkeyUsageLabel(passkey({ last_used_at: '2026-08-20T09:00:00Z' }))
    expect(label).toContain('last used')
    expect(label).not.toContain('never used')
  })
})
