import { afterEach, describe, expect, it, vi } from 'vitest'

import { AuthContext, type AuthValue, type CurrentUser } from '@/contexts/auth'
import { OIDC_CONFIG_KEY, PASSKEYS_KEY, TOTP_KEY, type Passkey } from '@/lib/clients/security'
import { renderScreen } from '@/test/renderScreen'

import { SecuritySettings } from './SecuritySettings'

// Static markup only: the ceremonies need clicks the render never fires, so
// this checks what the page says before any click, from a seeded cache.

function user(overrides: Partial<CurrentUser> = {}): CurrentUser {
  return {
    id: 'u-ada',
    email: 'ada@example.test',
    full_name: 'Ada',
    is_active: true,
    is_superuser: false,
    is_verified: true,
    locale: 'en',
    theme: 'dark',
    privacy_mode: false,
    swipe_left_action: 'menu',
    swipe_right_action: 'review',
    last_login_at: '2026-08-20T09:00:00Z',
    must_change_password: false,
    has_password: true,
    has_totp: false,
    has_oidc: false,
    ...overrides,
  }
}

const AUTH = (currentUser: CurrentUser): AuthValue => ({
  user: currentUser,
  status: 'authenticated',
  signIn: () => Promise.resolve({ mfaToken: null, methods: [] }),
  completeSecondFactor: () => Promise.resolve(),
  adoptToken: () => Promise.resolve(),
  signOut: () => Promise.resolve(),
  changePassword: () => Promise.resolve(),
})

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

function render({
  currentUser = user(),
  passkeys = [] as Passkey[],
  totp = { enabled: false, recovery_codes_remaining: 0 },
  oidc = { enabled: false, provider_name: 'Okta' },
}: {
  currentUser?: CurrentUser
  passkeys?: Passkey[]
  totp?: { enabled: boolean; recovery_codes_remaining: number }
  oidc?: { enabled: boolean; provider_name: string }
} = {}) {
  return renderScreen(
    <AuthContext value={AUTH(currentUser)}>
      <SecuritySettings />
    </AuthContext>,
    {
      seed: [
        [PASSKEYS_KEY, passkeys],
        [TOTP_KEY, totp],
        [OIDC_CONFIG_KEY, oidc],
      ],
    },
  )
}

afterEach(() => vi.unstubAllGlobals())

describe('passkeys', () => {
  it('says HTTPS is required and offers no add button over plain HTTP', () => {
    vi.stubGlobal('window', { isSecureContext: false })
    const markup = render()
    expect(markup).toContain('Passkeys need HTTPS')
    expect(markup).not.toContain('Add a passkey')
  })

  it('offers to add one, and lists what is already enrolled, over HTTPS', () => {
    vi.stubGlobal('window', { isSecureContext: true })
    const markup = render({ passkeys: [passkey({ name: 'Work YubiKey' })] })
    expect(markup).toContain('Add a passkey')
    expect(markup).not.toContain('Passkeys need HTTPS')
    expect(markup).toContain('Work YubiKey')
    expect(markup).toContain('never used')
  })

  it('names the day a passkey last signed somebody in', () => {
    vi.stubGlobal('window', { isSecureContext: true })
    const markup = render({
      passkeys: [passkey({ last_used_at: '2026-08-20T09:00:00Z' })],
    })
    expect(markup).toContain('last used')
  })

  it('says plainly that none are enrolled yet', () => {
    vi.stubGlobal('window', { isSecureContext: true })
    const markup = render({ passkeys: [] })
    expect(markup).toContain('No passkeys enrolled yet.')
  })
})

describe('two-factor authentication', () => {
  it('offers to turn it on, and says a password alone is enough while it is off', () => {
    const markup = render({ totp: { enabled: false, recovery_codes_remaining: 0 } })
    expect(markup).toContain('Turn on')
    expect(markup).toContain('A password alone signs in while this is off.')
    expect(markup).not.toContain('Turn off')
  })

  it('reports recovery codes remaining once it is on', () => {
    const markup = render({ totp: { enabled: true, recovery_codes_remaining: 3 } })
    expect(markup).toContain('Turn off')
    expect(markup).toContain('3 recovery codes left')
  })
})

describe('single sign-on', () => {
  it('says it is not configured when the server has no provider', () => {
    const markup = render({ oidc: { enabled: false, provider_name: '' } })
    expect(markup).toContain('Not configured on this server.')
  })

  it('points at the login page to link an account that has not linked yet', () => {
    const markup = render({
      currentUser: user({ has_oidc: false }),
      oidc: { enabled: true, provider_name: 'Okta' },
    })
    expect(markup).toContain('Not linked. Sign in with')
    expect(markup).toContain('Okta')
  })

  it('says the account is linked once it has signed in that way', () => {
    const markup = render({
      currentUser: user({ has_oidc: true }),
      oidc: { enabled: true, provider_name: 'Okta' },
    })
    expect(markup).toContain('Linked to Okta.')
  })
})
