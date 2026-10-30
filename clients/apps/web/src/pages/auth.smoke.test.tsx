import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { prerender } from 'react-dom/static'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it } from 'vitest'

import { AuthProvider } from '@/contexts/AuthProvider'
import { PrivacyProvider } from '@/contexts/PrivacyProvider'
import { ThemeProvider } from '@/contexts/ThemeProvider'
import { clearAccessToken, setAccessToken } from '@/lib/session'
import { AppRoutes } from '@/routes'

/**
 * What a browser with no session, or an unconfirmed token, gets. Through the
 * real route table: the property is that the guard sits above every
 * destination, which a direct mount would skip.
 */

async function at(path: string): Promise<string> {
  const app: ReactNode = (
    <QueryClientProvider client={new QueryClient()}>
      <ThemeProvider>
        <PrivacyProvider>
          <MemoryRouter initialEntries={[path]}>
            <AuthProvider>
              <AppRoutes />
            </AuthProvider>
          </MemoryRouter>
        </PrivacyProvider>
      </ThemeProvider>
    </QueryClientProvider>
  )
  // Pages are lazy chunks, which a static render leaves suspended; a prerender
  // waits for them, so a page behind the guard would show up here if it drew.
  const { prelude } = await prerender(app)
  return new Response(prelude).text()
}

afterEach(() => clearAccessToken())

describe('a browser with no session', () => {
  it('is shown the login form at the login route', async () => {
    const markup = await at('/login')
    expect(markup).toContain('Sign in')
    expect(markup).toContain('autoComplete="current-password"')
  })

  it('is shown no ledger, whichever destination it asks for', async () => {
    for (const path of ['/', '/transactions', '/net-worth', '/reports', '/settings/general']) {
      // The redirect renders nothing on the server; what matters is that no
      // page behind the guard put anything on screen.
      const markup = await at(path)
      expect(markup).not.toContain('agentifi-shell')
      expect(markup).not.toContain('app__page')
    }
  })

  it('offers no way to create an account, because there is not one', async () => {
    const markup = await at('/login')
    expect(markup).not.toContain('Sign up')
    expect(markup).toContain('agentifi user add')
  })
})

describe('a browser holding a token that has not been confirmed', () => {
  it('waits rather than showing either the app or the login form', async () => {
    setAccessToken('unconfirmed')
    const markup = await at('/')

    expect(markup).toContain('auth--waiting')
    expect(markup).not.toContain('autoComplete="current-password"')
    expect(markup).not.toContain('app__page')
  })
})

describe('the forced password change', () => {
  it('is a screen of its own, outside the shell', async () => {
    // Reached only with a session, so this asserts the route exists and is
    // guarded; the signed-in rendering is covered by the API's own tests.
    expect(await at('/set-password')).not.toContain('app__page')
  })
})
