import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { AuthContext, type AuthValue } from '@/contexts/auth'
import { ApiError } from '@/lib/api'

import { RequireAuth } from './RequireAuth'

/**
 * The gate in front of every screen, and its four answers. `error` must give
 * something to read and press, not an endless spinner.
 */
const BASE: AuthValue = {
  user: null,
  status: 'loading',
  signIn: () => Promise.resolve({ mfaToken: null, methods: [] }),
  completeSecondFactor: () => Promise.resolve(),
  adoptToken: () => Promise.resolve(),
  signOut: () => Promise.resolve(),
  changePassword: () => Promise.resolve(),
}

function render(value: Partial<AuthValue>): string {
  return renderToStaticMarkup(
    <MemoryRouter initialEntries={['/']}>
      <AuthContext value={{ ...BASE, ...value }}>
        <Routes>
          <Route element={<RequireAuth />}>
            <Route path="*" element={<p>the page</p>} />
          </Route>
        </Routes>
      </AuthContext>
    </MemoryRouter>,
  )
}

describe('<RequireAuth>', () => {
  it('holds a blank while a stored token is being confirmed', () => {
    const markup = render({ status: 'loading' })
    expect(markup).toMatch(/class="[^"]*\bspinner\b/)
    expect(markup).not.toContain('the page')
  })

  it('renders the page once the session is confirmed', () => {
    expect(render({ status: 'authenticated' })).toContain('the page')
  })

  it('renders nothing of the app for somebody signed out', () => {
    expect(render({ status: 'anonymous' })).not.toContain('the page')
  })

  it('says the server is unreachable, and offers both ways out', () => {
    const markup = render({ status: 'error', error: new ApiError(500, '/auth/me', null) })
    expect(markup).toContain('Could not reach the server')
    expect(markup).toContain('Try again')
    expect(markup).toContain('Sign out')
    expect(markup).not.toMatch(/class="[^"]*\bspinner\b/)
  })

  it('names the one cause the user can act on', () => {
    // A 403 on `/auth/me` is a disabled account; signing in again will not
    // change it.
    const markup = render({
      status: 'error',
      error: new ApiError(403, '/auth/me', { detail: 'This account is inactive' }),
    })
    expect(markup).toContain('Your account is inactive')
  })

  it('quotes the request id, which is what finds the line in the log', () => {
    const markup = render({
      status: 'error',
      error: new ApiError(503, '/auth/me', null, 'req-8f2c'),
    })
    expect(markup).toContain('req-8f2c')
  })

  it('says the same thing when the request never reached a server at all', () => {
    const markup = render({ status: 'error', error: new TypeError('Failed to fetch') })
    expect(markup).toContain('Could not reach the server')
  })
})
