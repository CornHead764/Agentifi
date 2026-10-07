import type { QueryKey } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'

import { AuthContext, type AuthValue, type CurrentUser } from '@/contexts/auth'
import {
  CURRENT_SPACE_KEY,
  membersKey,
  SPACES_KEY,
  type Member,
  type Role,
  type Space,
} from '@/lib/clients/spaces'
import { renderScreen } from '@/test/renderScreen'

import { SpacesSettings } from './SpacesSettings'

const ME: CurrentUser = {
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
  last_login_at: null,
  must_change_password: false,
  has_password: true,
  has_totp: false,
  has_oidc: false,
}

const AUTH: AuthValue = {
  user: ME,
  status: 'authenticated',
  signIn: () => Promise.resolve({ mfaToken: null, methods: [] }),
  completeSecondFactor: () => Promise.resolve(),
  adoptToken: () => Promise.resolve(),
  signOut: () => Promise.resolve(),
  changePassword: () => Promise.resolve(),
}

function space(overrides: Partial<Space> = {}): Space {
  return {
    id: 's1',
    name: 'Household',
    primary_currency: 'USD',
    timezone: 'UTC',
    default_date_range: '',
    sidebar_account_types: null,
    role: 'owner',
    can_write: true,
    is_owner: true,
    joined_at: '2026-01-02T09:00:00Z',
    ...overrides,
  }
}

function member(id: string, role: Role, accepted: boolean, overrides: Partial<Member> = {}): Member {
  return {
    id,
    user_id: `u-${id}`,
    email: `${id}@example.test`,
    full_name: id === 'ada' ? 'Ada' : id.toUpperCase(),
    role,
    invited_at: '2026-08-01T09:00:00Z',
    accepted_at: accepted ? '2026-08-02T09:00:00Z' : null,
    ...overrides,
  }
}

function render(spaces: Space[], members: Member[]) {
  return renderScreen(
    <AuthContext value={AUTH}>
      <SpacesSettings />
    </AuthContext>,
    {
      seed: [
        [SPACES_KEY, spaces],
        [CURRENT_SPACE_KEY, spaces[0]],
        ...spaces.map((one): [QueryKey, Member[]] => [membersKey(one.id), members]),
      ],
    },
  )
}

const OWNER = member('ada', 'owner', true)
const VIEWER = member('bob', 'viewer', true)
const INVITED = member('cid', 'member', false)

describe('the spaces and sharing settings page', () => {
  it('names the space the rest of the app is reading', () => {
    const markup = render([space(), space({ id: 's2', name: 'Cabin', role: 'member' })], [OWNER])
    expect(markup).toContain('Household')
    expect(markup).toContain('Active')
    expect(markup).toContain('Every other screen is reading this space.')
  })

  it('offers a way to make another space the active one', () => {
    // Another space must be switchable to, not just listed.
    const markup = render([space(), space({ id: 's2', name: 'Cabin', role: 'member' })], [OWNER])
    expect(markup).toContain('Switch')
  })

  it('does not offer to switch to the space already being read', () => {
    expect(render([space()], [OWNER])).not.toContain('Switch')
  })

  it('opens no dialog until something is chosen', () => {
    // Every dialog here writes or confirms a removal. One rendered on arrival
    // is a modal nobody asked for.
    const markup = render([space()], [OWNER])
    expect(markup).not.toContain('Create space')
    expect(markup).not.toContain('Send invitation')
    expect(markup).not.toContain('You lose access')
  })

  it('shows an invitation as pending rather than as access already granted', () => {
    const markup = render([space()], [OWNER, INVITED])
    expect(markup).toContain('Invited')
    expect(markup).toContain('No access until accepted')
  })

  it('sorts the people so an invitation never leads the list', () => {
    const markup = render([space()], [INVITED, OWNER])
    expect(markup.indexOf('ada@example.test')).toBeLessThan(markup.indexOf('cid@example.test'))
  })

  it('gives a viewer the screen read-only rather than controls that would 403', () => {
    // Only an owner may change membership. A role picker here would be a
    // control whose every use comes back a 403.
    const readOnly = space({ role: 'viewer', can_write: false, is_owner: false })
    const markup = render([readOnly], [OWNER, VIEWER])
    expect(markup).toContain('Only an owner or admin can change who is here.')
    expect(markup).not.toContain('Role for')
    expect(markup).not.toContain('Invite')
    expect(markup).toContain('Viewer')
  })

  it('lets an owner change everybody else and offers the invite', () => {
    const markup = render([space()], [OWNER, VIEWER])
    expect(markup).toContain('Invite')
    expect(markup).toContain('Role for BOB')
  })

  it('offers an owner an invitation only, and says who creates logins', () => {
    const markup = render([space()], [OWNER, VIEWER])
    expect(markup).not.toContain('Create login')
    expect(markup).toContain('The server administrator creates logins.')
  })

  it('offers an admin the invite too', () => {
    const markup = render([space({ role: 'admin', is_owner: true })], [OWNER, VIEWER])
    expect(markup).toContain('Invite')
    expect(markup).not.toContain('Role for Ada')
  })

  it('offers the last owner no way to demote themselves', () => {
    // A space with no accepted owner can never be shared or recovered again:
    // membership is the only way in and only an owner hands one out.
    const markup = render([space()], [OWNER, VIEWER])
    expect(markup).toContain('The last owner. Make somebody else an owner first.')
    expect(markup).not.toContain('Role for Ada')
  })

  it('lets the outgoing owner move once somebody else has accepted', () => {
    const markup = render([space()], [OWNER, member('cid', 'owner', true)])
    expect(markup).not.toContain('The last owner.')
    expect(markup).toContain('Role for Ada')
  })

  it('does not count an invited owner as the successor', () => {
    const markup = render([space()], [OWNER, member('cid', 'owner', false)])
    expect(markup).toContain('The last owner. Make somebody else an owner first.')
  })
})
