/** The account's theme and privacy mode are adopted during render, so a static render can check them. */

import { renderToStaticMarkup } from 'react-dom/server'
import { afterEach, describe, expect, it } from 'vitest'

import {
  publishRoamingPreferences,
  resetRoamingPreferences,
  type RoamingPreferences,
} from '@/lib/preferences'

import { PrivacyProvider } from './PrivacyProvider'
import { ThemeProvider } from './ThemeProvider'
import { usePrivacy } from './privacy'
import { useTheme } from './theme'

function account(overrides: Partial<RoamingPreferences> = {}): RoamingPreferences {
  return {
    userId: 'u-ada',
    theme: 'dark',
    privacyMode: false,
    swipeLeft: 'menu',
    swipeRight: 'review',
    animationMs: 160,
    toastMs: 6000,
    ...overrides,
  }
}

function ThemeProbe() {
  return <span>{useTheme().preference}</span>
}

function PrivacyProbe() {
  return <span>{usePrivacy().hidden ? 'hidden' : 'shown'}</span>
}

afterEach(() => resetRoamingPreferences())

describe('the theme provider', () => {
  it('falls back to dark when nothing is stored and nobody is signed in', () => {
    expect(
      renderToStaticMarkup(
        <ThemeProvider>
          <ThemeProbe />
        </ThemeProvider>,
      ),
    ).toContain('dark')
  })

  it('adopts the theme the account carries', () => {
    publishRoamingPreferences(account({ theme: 'light', privacyMode: false }))
    expect(
      renderToStaticMarkup(
        <ThemeProvider>
          <ThemeProbe />
        </ThemeProvider>,
      ),
    ).toContain('light')
  })

  it('ignores a theme this build has no branch for', () => {
    publishRoamingPreferences(account({ theme: 'sepia', privacyMode: false }))
    expect(
      renderToStaticMarkup(
        <ThemeProvider>
          <ThemeProbe />
        </ThemeProvider>,
      ),
    ).toContain('dark')
  })
})

describe('the privacy provider', () => {
  it('shows values when nobody is signed in', () => {
    expect(
      renderToStaticMarkup(
        <PrivacyProvider>
          <PrivacyProbe />
        </PrivacyProvider>,
      ),
    ).toContain('shown')
  })

  it('adopts privacy mode from the account', () => {
    publishRoamingPreferences(account({ theme: 'dark', privacyMode: true }))
    expect(
      renderToStaticMarkup(
        <PrivacyProvider>
          <PrivacyProbe />
        </PrivacyProvider>,
      ),
    ).toContain('hidden')
  })
})
