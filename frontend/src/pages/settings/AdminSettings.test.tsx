import { describe, expect, it } from 'vitest'

import { AuthContext, type AuthValue, type CurrentUser } from '@/contexts/auth'
import {
  ADMIN_OIDC_KEY,
  ADMIN_SPACES_KEY,
  ADMIN_USERS_KEY,
  lastAdministrator,
  newUserBody,
  signInMethods,
  type AdminSpace,
  type AdminUser,
  type NewUserForm,
  type OidcSettings,
} from '@/lib/clients/admin'
import { renderScreen } from '@/test/renderScreen'

import { AdminSettings } from './AdminSettings'

function person(overrides: Partial<CurrentUser> = {}): CurrentUser {
  return {
    id: 'u-ada',
    email: 'ada@example.test',
    full_name: 'Ada',
    is_active: true,
    is_superuser: true,
    is_verified: true,
    locale: 'en',
    theme: 'dark',
    privacy_mode: false,
    swipe_left_action: 'menu',
    swipe_right_action: 'review',
    last_login_at: null,
    must_change_password: false,
    has_password: true,
    has_totp: false,
    has_oidc: false,
    ...overrides,
  }
}

function auth(user: CurrentUser | null): AuthValue {
  return {
    user,
    status: user ? 'authenticated' : 'anonymous',
    signIn: () => Promise.resolve({ mfaToken: null, methods: [] }),
    completeSecondFactor: () => Promise.resolve(),
    adoptToken: () => Promise.resolve(),
    signOut: () => Promise.resolve(),
    changePassword: () => Promise.resolve(),
  }
}

function user(overrides: Partial<AdminUser> = {}): AdminUser {
  return {
    id: 'u-bob',
    email: 'bob@example.test',
    full_name: 'Bob',
    is_active: true,
    is_superuser: false,
    is_verified: true,
    must_change_password: false,
    created_at: '2026-01-02T09:00:00Z',
    last_login_at: null,
    has_password: true,
    has_totp: false,
    has_oidc: false,
    oidc_issuer: '',
    passkey_count: 0,
    memberships: [
      {
        id: 'm1',
        space_id: 's1',
        space_name: 'Household',
        role: 'member',
        accepted: true,
      },
    ],
    ...overrides,
  }
}

function space(overrides: Partial<AdminSpace> = {}): AdminSpace {
  return {
    id: 's1',
    name: 'Household',
    primary_currency: 'USD',
    timezone: 'UTC',
    created_at: '2026-01-02T09:00:00Z',
    members: [
      {
        membership_id: 'm1',
        user_id: 'u-bob',
        email: 'bob@example.test',
        full_name: 'Bob',
        role: 'member',
        accepted: true,
      },
    ],
    ...overrides,
  }
}

function oidc(overrides: Partial<OidcSettings> = {}): OidcSettings {
  return {
    enabled: false,
    provider_name: 'OIDC',
    discovery_url: '',
    client_id: '',
    has_client_secret: false,
    scopes: ['openid', 'email', 'profile'],
    auto_register: false,
    require_verified_email: true,
    link_existing_email: false,
    sources: {
      enabled: 'environment',
      provider_name: 'environment',
      discovery_url: 'environment',
      client_id: 'environment',
      client_secret: 'environment',
      scopes: 'environment',
      auto_register: 'environment',
      require_verified_email: 'environment',
      link_existing_email: 'environment',
    },
    callback_url: 'https://money.example.test/auth/oidc/callback',
    configured: false,
    ...overrides,
  }
}

function render(
  me: CurrentUser | null,
  users: AdminUser[] = [user()],
  spaces: AdminSpace[] = [space()],
  settings: OidcSettings = oidc(),
) {
  return renderScreen(
    <AuthContext value={auth(me)}>
      <AdminSettings />
    </AuthContext>,
    {
      seed: [
        [ADMIN_USERS_KEY, users],
        [ADMIN_SPACES_KEY, spaces],
        [ADMIN_OIDC_KEY, settings],
      ],
    },
  )
}

describe('the server admin settings page', () => {
  it('lists every account with how they sign in and where they are', () => {
    const markup = render(person())
    expect(markup).toContain('bob@example.test')
    expect(markup).toContain('Password')
    expect(markup).toContain('Household')
    expect(markup).toContain('Member')
  })

  it('refuses an account that does not administer the server', () => {
    // Hiding the nav entry is a courtesy; this is what somebody who typed the
    // URL or followed a bookmark sees.
    const markup = render(person({ is_superuser: false }))
    expect(markup).toContain('Not available')
    expect(markup).not.toContain('bob@example.test')
    expect(markup).not.toContain('Add user')
  })

  it('renders neither the screen nor the refusal until the account is known', () => {
    // `user` is null while /auth/me is in flight. Showing the refusal over a
    // guess would flash it at the administrator on every visit.
    const markup = render(null)
    expect(markup).not.toContain('Not available')
    expect(markup).not.toContain('bob@example.test')
    expect(markup).not.toContain('Single sign-on')
  })

  it('opens no dialog until something is chosen', () => {
    const markup = render(person())
    expect(markup).not.toContain('Add a user')
    expect(markup).not.toContain('Copy this password now')
  })

  it('names an account that is in no space, rather than leaving the cell blank', () => {
    const markup = render(person(), [user({ memberships: [] })])
    expect(markup).toContain('In no space')
  })

  it('marks an invitation as one rather than as access already granted', () => {
    const markup = render(person(), [
      user({
        memberships: [
          { id: 'm2', space_id: 's2', space_name: 'Cabin', role: 'viewer', accepted: false },
        ],
      }),
    ])
    expect(markup).toContain('(invited)')
  })

  it('gives the callback URL to copy and says a value comes from the environment', () => {
    const markup = render(person())
    expect(markup).toContain('https://money.example.test/auth/oidc/callback')
    expect(markup).toContain('Coming from the environment.')
  })

  it('never renders the stored client secret', () => {
    const markup = render(person(), [user()], [space()], oidc({ has_client_secret: true }))
    expect(markup).toContain('A secret is stored and never shown here')
    expect(markup).not.toContain('value="a-real-secret"')
  })
})

describe('the administrator guards', () => {
  const admin = user({ id: 'u-ada', email: 'ada@example.test', is_superuser: true })

  it('calls an account the last administrator when nobody else is one', () => {
    expect(lastAdministrator([admin, user()], admin)).toBe(true)
  })

  it('does not count an administrator who cannot sign in', () => {
    const inactive = user({ id: 'u-cid', is_superuser: true, is_active: false })
    expect(lastAdministrator([admin, inactive], admin)).toBe(true)
  })

  it('lets go once a second administrator is active', () => {
    const second = user({ id: 'u-cid', is_superuser: true })
    expect(lastAdministrator([admin, second], admin)).toBe(false)
  })

  it('badges an administrator so a row says what the guards are about', () => {
    // The disabled menu items are in a Radix portal static markup cannot reach;
    // `lastAdministrator` is pinned by the tests above.
    expect(render(person(), [admin])).toContain('Admin')
  })
})

describe('the add-user request', () => {
  const form: NewUserForm = {
    email: '  new@example.test ',
    full_name: ' New Person ',
    password: '',
    must_change_password: true,
    is_superuser: false,
    placement: 'new',
    space_name: ' Their House ',
    currency: 'usd',
    space_id: 's1',
    role: 'member',
  }

  it('omits an empty password so the server mints one', () => {
    const body = newUserBody(form)
    expect(body).not.toHaveProperty('password')
    expect(body.email).toBe('new@example.test')
    expect(body.full_name).toBe('New Person')
    expect(body.must_change_password).toBe(true)
  })

  it('sends a space to create and nothing about one that exists', () => {
    const body = newUserBody(form)
    expect(body.space_name).toBe('Their House')
    expect(body.currency).toBe('USD')
    // Sending both would leave the server picking, and the one it picked would
    // be the one nobody meant.
    expect(body).not.toHaveProperty('space_id')
    expect(body).not.toHaveProperty('role')
  })

  it('sends a space to join and nothing about one to create', () => {
    const body = newUserBody({ ...form, placement: 'existing', role: 'viewer' })
    expect(body.space_id).toBe('s1')
    expect(body.role).toBe('viewer')
    expect(body).not.toHaveProperty('space_name')
    expect(body).not.toHaveProperty('currency')
  })

  it('sends a typed password as typed', () => {
    const body = newUserBody({ ...form, password: 'a long enough password' })
    expect(body.password).toBe('a long enough password')
  })
})

describe('signInMethods', () => {
  it('names the ways in rather than counting them', () => {
    expect(signInMethods(user({ passkey_count: 2, has_totp: true }))).toBe(
      'Password · 2 passkeys · Two-factor',
    )
  })

  it('says so when an account has no way in at all', () => {
    expect(signInMethods(user({ has_password: false }))).toBe('No way in yet')
  })
})
