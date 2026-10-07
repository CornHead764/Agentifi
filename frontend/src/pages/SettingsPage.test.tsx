import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { SETTINGS_SECTIONS } from '@/components/shell/destinations'
import { AuthContext, type AuthValue, type CurrentUser } from '@/contexts/auth'

import { SettingsPage } from './SettingsPage'

/**
 * Server admin is listed for a superuser only. A courtesy, not the access
 * control: the API refuses /admin to an ordinary account and the page refuses
 * to render.
 */

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

function navAs(user: CurrentUser | null): string {
  return renderToStaticMarkup(
    <AuthContext value={auth(user)}>
      <MemoryRouter initialEntries={['/settings/general']}>
        <SettingsPage />
      </MemoryRouter>
    </AuthContext>,
  )
}

/** Every anchor in the render, as raw tag text. */
function anchors(html: string): string[] {
  return html.match(/<a\b[^>]*>/g) ?? []
}

describe('<SettingsPage>', () => {
  it('lists every ordinary section for an ordinary account', () => {
    const hrefs = anchors(navAs(ME)).map((tag) => /href="([^"]+)"/.exec(tag)?.[1])
    expect(hrefs).toEqual(
      SETTINGS_SECTIONS.filter((section) => !('superuser' in section)).map(
        (section) => section.path,
      ),
    )
  })

  it('lists the merchants as one section rather than one per shop', () => {
    // Amazon and Costco are one connector; the shop is a picker inside the
    // section driven by MERCHANTS, so a third merchant needs no edit here.
    const markup = navAs(ME)
    expect(markup).toContain('href="/settings/merchants"')
    expect(markup).toContain('Merchants')
    expect(markup).not.toContain('href="/settings/amazon"')
    expect(markup).not.toContain('href="/settings/costco"')
  })

  it('calls the bill section by what it lists, at the address notifications link to', () => {
    // A push about a code request opens /settings/bills?challenge=…, so the
    // name can change and the path cannot.
    const markup = navAs(ME)
    expect(markup).toContain('href="/settings/bills"')
    expect(markup).toContain('Bill providers')
    expect(markup).not.toContain('Bill sign-ins')
  })

  it('lists only settings sections, not pages the main navigation already reaches', () => {
    const markup = navAs(ME)
    for (const page of ['/rules', '/upcoming/recurring', '/assistant', '/settings/assistant']) {
      expect(markup).not.toContain(`href="${page}"`)
    }
    expect(anchors(markup).every((tag) => tag.includes('href="/settings/'))).toBe(true)
  })

  it('keeps server admin out of an ordinary account`s nav', () => {
    expect(navAs(ME)).not.toContain('/settings/admin')
    expect(navAs(ME)).not.toContain('Server admin')
  })

  it('lists server admin for the account that administers the server', () => {
    const markup = navAs({ ...ME, is_superuser: true })
    expect(markup).toContain('href="/settings/admin"')
    expect(markup).toContain('Server admin')
  })

  it('leaves it out while the account is still unknown', () => {
    // `user` is null until /auth/me answers. Listing it and taking it away
    // again is worse than listing it a round trip late.
    expect(navAs(null)).not.toContain('/settings/admin')
  })
})
