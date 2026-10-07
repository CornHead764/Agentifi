/**
 * A server with no accounts offers to make the first one on the sign-in card,
 * and says so; one with an account offers only sign-in.
 */

import { afterEach, describe, expect, it } from 'vitest'

import { AuthProvider } from '@/contexts/AuthProvider'
import { FIRST_ACCOUNT_KEY } from '@/lib/clients/security'
import { clearAccessToken } from '@/lib/session'
import { renderScreen } from '@/test/renderScreen'

import { LoginPage } from './LoginPage'

function login(open: boolean): string {
  return renderScreen(
    <AuthProvider>
      <LoginPage />
    </AuthProvider>,
    { seed: [[FIRST_ACCOUNT_KEY, { open }]], route: '/login' },
  )
}

afterEach(() => clearAccessToken())

describe('the sign-in card', () => {
  it('offers the first account on a server with none', () => {
    const markup = login(true)
    expect(markup).toContain('This server has no accounts yet')
    expect(markup).toContain('Create account')
    expect(markup).toContain('autoComplete="new-password"')
    expect(markup).not.toContain('Sign in with a passkey')
  })

  it('recommends importing Simplifi history before connecting SimpleFIN', () => {
    const markup = login(true)
    expect(markup).toContain('Bringing history from Quicken Simplifi?')
    expect(markup).toContain('Import it before you connect')
  })

  it('offers only sign-in once an account exists', () => {
    const markup = login(false)
    expect(markup).not.toContain('Create account')
    expect(markup).toContain('Sign in to Agentifi')
  })
})
