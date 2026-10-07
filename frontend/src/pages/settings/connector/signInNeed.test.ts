import { describe, expect, it } from 'vitest'

import { forgetPasswordLabel, signInLabel, signInNeed } from './signInNeed'

describe('what a login waits on to sign in', () => {
  it('reads a pause before the flag, which a failed run clears', () => {
    const paused = { needsSignIn: false, paused: 'code_needed', hasPassword: true }
    expect(signInNeed(paused)).toBe('code-needed')
    expect(signInNeed({ ...paused, paused: 'password_refused' })).toBe('password-refused')
  })

  it('lets a kept password sign in on its own and asks otherwise', () => {
    const lapsed = { needsSignIn: true, paused: '', hasPassword: true }
    expect(signInNeed(lapsed)).toBe('password-signs-in')
    expect(signInNeed({ ...lapsed, hasPassword: false })).toBe('sign-in')
    expect(signInNeed({ ...lapsed, needsSignIn: false })).toBeNull()
  })

  it('says again for a login that was signed in to or stopped at one', () => {
    expect(signInLabel(false, null)).toBe('Sign in')
    expect(signInLabel(true, null)).toBe('Sign in again')
    expect(signInLabel(false, 'code-needed')).toBe('Sign in again')
  })

  it('names the key beside the password it is sealed with', () => {
    expect(forgetPasswordLabel(false)).toBe('Forget password')
    expect(forgetPasswordLabel(true)).toBe('Forget password and key')
  })
})
