import { Route, Routes } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { AuthContext, type AuthValue, type CurrentUser } from '@/contexts/auth'
import { AuthProvider } from '@/contexts/AuthProvider'
import { ThemeProvider } from '@/contexts/ThemeProvider'
import { CURRENT_SPACE_KEY, SPACES_KEY, type Space } from '@/lib/clients/spaces'
import { moneyFromCents } from '@/lib/money'
import { renderScreen } from '@/test/renderScreen'

import { AppShell } from './AppShell'
import type { AccountNode } from './shell/accountTree'

const TREE: AccountNode[] = [
  {
    id: 'banking',
    name: 'Banking',
    kind: 'class',
    children: [
      {
        id: 'cash',
        name: 'Cash & Checking',
        kind: 'group',
        children: [
          {
            id: 'joint',
            name: 'Everyday Checking',
            kind: 'account',
            balance: moneyFromCents(512_345),
          },
        ],
      },
    ],
  },
]

// The header carries the account menu, so the shell needs a session context.
// With no token the provider resolves to anonymous without a request.
function shellAt(path: string): string {
  return renderScreen(
    <ThemeProvider>
      <AuthProvider>
        <Routes>
          <Route element={<AppShell accounts={{ tree: TREE }} />}>
            <Route path="*" element={<p>page</p>} />
          </Route>
        </Routes>
      </AuthProvider>
    </ThemeProvider>,
    { route: path },
  )
}

const ADA: CurrentUser = {
  id: 'u-ada',
  email: 'ada@example.test',
  full_name: 'Ada Lovelace',
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
  user: ADA,
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

/** The shell for a signed-in account, with its spaces already answered. */
function shellFor(spaces: Space[], path = '/'): string {
  return renderScreen(
    <ThemeProvider>
      <AuthContext value={AUTH}>
        <Routes>
          <Route element={<AppShell accounts={{ tree: TREE }} />}>
            <Route path="*" element={<p>page</p>} />
          </Route>
        </Routes>
      </AuthContext>
    </ThemeProvider>,
    {
      route: path,
      seed: spaces[0]
        ? [
            [SPACES_KEY, spaces],
            [CURRENT_SPACE_KEY, spaces[0]],
          ]
        : [[SPACES_KEY, spaces]],
    },
  )
}

describe('<AppShell>', () => {
  it('carries the accounts drawer on the dashboard and the register', () => {
    expect(shellAt('/')).toContain('All Accounts')
    expect(shellAt('/transactions')).toContain('All Accounts')
  })

  it('drops the drawer everywhere else, because those pages are full width', () => {
    expect(shellAt('/net-worth')).not.toContain('All Accounts')
    expect(shellAt('/settings/general')).not.toContain('All Accounts')
  })

  it('toggles the docked drawer from the top bar alone', () => {
    const html = shellAt('/')
    expect(html.match(/aria-label="Hide accounts"/g)).toHaveLength(1)
    expect(html).toContain('header__accounts')
  })

  it('rolls the tree up to a total on the root row', () => {
    expect(shellAt('/')).toContain('$5,123.45')
  })

  it('names the page in the header', () => {
    expect(shellAt('/reports')).toContain('Reports')
  })

  it('carries the phone tab bar, with the rest of the destinations behind More', () => {
    // The stylesheet decides which of rail and tab bar shows; both are in the
    // markup at every width so a resize needs no re-render to find its nav.
    const markup = shellAt('/reports')
    expect(markup).toContain('class="tabbar"')
    expect(markup).toContain('aria-label="More"')
    // Reports has no tab of its own, so More is the lit one.
    expect(markup).toMatch(/aria-current="page"[^>]*aria-label="More"|aria-label="More"[^>]*aria-current="page"/)
  })

  it('puts the account’s initials on the avatar', () => {
    // The shell passes the account's name to the avatar so it draws the
    // initials rather than a generic mark.
    expect(shellFor([space()])).toContain('AL')
  })

  it('names the active space only when there is another one to be in', () => {
    // Quoted whole: `header__spacer` is a substring of the chip's own class.
    expect(shellFor([space()])).not.toContain('class="header__space"')

    const two = shellFor([space(), space({ id: 's2', name: 'Cabin', role: 'member' })])
    expect(two).toContain('class="header__space"')
    expect(two).toContain('Household')
  })

  it('switches space from the chip itself rather than sending the reader to Settings', () => {
    const two = shellFor([space(), space({ id: 's2', name: 'Cabin', role: 'member' })])
    expect(two).toMatch(/<button[^>]*class="header__space"/)
    expect(two).toContain('Switch space')
    expect(two).not.toContain('switch in Settings')
  })

  it('offers the way into a space rather than a page that cannot load', () => {
    // With no accepted membership every panel would answer "Space not found".
    const markup = shellFor([])
    expect(markup).toContain('You are not in a space yet')
    expect(markup).toContain('New space')
    expect(markup).not.toContain('All Accounts')
    expect(markup).not.toContain('<p>page</p>')
  })
})
