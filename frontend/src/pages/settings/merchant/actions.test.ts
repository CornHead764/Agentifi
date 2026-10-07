import { describe, expect, it } from 'vitest'

import { accountActions, accountStopped, accountSignInNeed, passwordFact, signsInAs } from './actions'

const working = {
  connected: true,
  needs_sign_in: false,
  last_sync_status: 'ok' as const,
  has_password: false,
  sign_in_paused: '' as const,
  second_factor: '' as const,
}

/** A login whose session lapsed at the last update. */
const lapsed = { ...working, needs_sign_in: true, last_sync_status: 'needs_sign_in' as const }

describe('the one button a shop account shows, and its menu', () => {
  it('updates a signed-in account, with history and signing in again behind the menu', () => {
    expect(accountActions(working, true)).toEqual({
      primary: 'update',
      menu: ['history', 'connect', 'forget', 'edit', 'remove'],
    })
  })

  it('puts the sign-in on the row when one is missing, and nothing that needs one', () => {
    const { primary, menu } = accountActions(lapsed, true)
    expect(primary).toBe('connect')
    expect(menu).toEqual(['forget', 'edit', 'remove'])
  })

  it('offers no button to an account on a server that cannot sign in', () => {
    expect(accountActions({ ...working, connected: false }, false)).toEqual({
      primary: null,
      menu: ['edit', 'remove'],
    })
  })

  it('offers to forget a kept password beside the session', () => {
    expect(accountActions({ ...working, has_password: true }, true).menu).toEqual([
      'history',
      'connect',
      'forget-password',
      'forget',
      'edit',
      'remove',
    ])
  })

  it('updates a lapsed session whose kept password signs in, rather than asking for a sign-in', () => {
    const { primary, menu } = accountActions({ ...lapsed, has_password: true }, true)
    expect(primary).toBe('update')
    expect(menu).toEqual(['connect', 'forget-password', 'forget', 'edit', 'remove'])
  })

  it('asks for a sign-in once the kept password is paused, with one more try behind the menu', () => {
    for (const reason of ['password_refused', 'code_needed'] as const) {
      const { primary, menu } = accountActions(
        { ...lapsed, has_password: true, sign_in_paused: reason },
        true,
      )
      expect(primary).toBe('connect')
      expect(menu).toEqual(['update', 'forget-password', 'forget', 'edit', 'remove'])
    }
  })

  it('offers a backfill after history to a signed-in Amazon login with orders on a server with a browser', () => {
    const page = { invoices: 'page' as const, engine: true }
    expect(accountActions({ ...working, orders: 4 }, true, page).menu).toEqual([
      'history',
      'backfill',
      'connect',
      'forget',
      'edit',
      'remove',
    ])
    expect(accountActions({ ...working, orders: 0 }, true, page).menu).not.toContain('backfill')
    expect(accountActions({ ...lapsed, orders: 4 }, true, page).menu).not.toContain('backfill')
    expect(
      accountActions({ ...working, orders: 4 }, true, {
        ...page,
        engine: false,
      }).menu,
    ).not.toContain('backfill')
  })

  it('offers a laid-out backfill with no session, since it asks the merchant nothing', () => {
    const laidOut = { invoices: 'laid-out' as const, engine: true }
    expect(accountActions({ ...lapsed, orders: 2 }, true, laidOut).menu).toContain('backfill')
    expect(
      accountActions({ ...working, connected: false, orders: 2 }, false, laidOut).menu,
    ).toEqual(['backfill', 'edit', 'remove'])
  })

  it('counts a failed update as stopped, and a finished one with a warning as not', () => {
    expect(accountStopped({ needs_sign_in: false, last_sync_status: 'failed' })).toBe(true)
    expect(accountStopped({ needs_sign_in: false, last_sync_status: 'ok' })).toBe(false)
  })
})

describe('what a lapsed session is waiting on', () => {
  it('tells the four apart', () => {
    expect(accountSignInNeed(working)).toBeNull()
    expect(accountSignInNeed(lapsed)).toBe('sign-in')
    expect(accountSignInNeed({ ...lapsed, has_password: true })).toBe('password-signs-in')
    expect(
      accountSignInNeed({ ...lapsed, has_password: true, sign_in_paused: 'password_refused' }),
    ).toBe('password-refused')
    expect(
      accountSignInNeed({ ...lapsed, has_password: true, sign_in_paused: 'code_needed' }),
    ).toBe('code-needed')
  })

  it('stays paused after an update by hand fails some other way', () => {
    // A run lifts a pause only by getting in; a failed one clears the flag and
    // leaves the pause, so the scheduler still skips the account.
    const failedWhilePaused = {
      ...working,
      has_password: true,
      last_sync_status: 'failed' as const,
      sign_in_paused: 'password_refused' as const,
    }
    expect(accountSignInNeed(failedWhilePaused)).toBe('password-refused')
  })
})

describe('what the row says about the password', () => {
  it('says what is kept, and why a paused one is not tried', () => {
    expect(passwordFact({ ...working, has_totp: false }, 'Amazon')).toBe(
      'Only the signed-in session is kept, so a sign-in is needed whenever it lapses.',
    )
    expect(passwordFact({ ...working, has_password: true, has_totp: false }, 'Amazon')).toBe(
      'Password kept, encrypted, so updates sign in on their own.',
    )
    expect(passwordFact({ ...working, has_password: true, has_totp: true }, 'Amazon')).toBe(
      'Password and authenticator key kept, encrypted, so updates sign in on their own.',
    )
    expect(
      passwordFact(
        { ...working, has_password: true, has_totp: false, second_factor: 'email' },
        'Costco',
      ),
    ).toContain('codes are read from the billing mailbox')
    expect(
      passwordFact(
        { ...lapsed, has_password: true, has_totp: false, sign_in_paused: 'password_refused' },
        'Costco',
      ),
    ).toContain('Costco turned it away at the last update')
    expect(
      passwordFact(
        { ...lapsed, has_password: true, has_totp: false, sign_in_paused: 'code_needed' },
        'Amazon',
      ),
    ).toContain('Amazon asked for a code at the last update')
    expect(
      passwordFact({ ...working, connected: false, has_password: false, has_totp: false }, 'Amazon'),
    ).toBeNull()
  })
})

describe('the login an account signs in with', () => {
  it('is said only when the name does not already say it', () => {
    expect(signsInAs({ name: 'Alex', email: 'alex@example.com' })).toBe('alex@example.com')
    expect(signsInAs({ name: 'alex@example.com', email: 'alex@example.com' })).toBeNull()
    expect(signsInAs({ name: 'Alex', email: 'alex' })).toBeNull()
    expect(signsInAs({ name: 'Alex', email: '' })).toBeNull()
  })
})
