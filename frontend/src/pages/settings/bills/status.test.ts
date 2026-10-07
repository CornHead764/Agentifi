import { describe, expect, it } from 'vitest'

import type {
  BillAgentStatus,
  BillChallenge,
  BillConnection,
  BillProviderInfo,
} from '@/lib/clients/bills'

import {
  cardAction,
  connectionActions,
  menuActionLabel,
  scheduleNote,
  signInBlocked,
  connectionSignInNeed,
  waitingChallengeFor,
} from './status'

const connection: BillConnection = {
  id: 'conn-power',
  biller: 'we-energies',
  label: 'Main account',
  username: 'somebody',
  site: '',
  credential_source: 'session',
  has_totp: false,
  second_factor: '' as const,
  connected: true,
  signed_in_at: '2026-09-10T02:00:00Z',
  needs_sign_in: false,
  sign_in_paused: '',
  autopay_rule: 'none',
  autopay_days: null,
  autopay_day: null,
  autopay_account_id: null,
  pull_enabled: true,
  pull_at: null,
  last_pulled_at: '2026-09-17T09:00:00Z',
  last_pull_status: 'ok',
  last_pull_error: '',
  has_failure_screenshot: false,
  has_trail: false,
  pulling: false,
  created_at: '2026-09-01T00:00:00Z',
}

const challenge: BillChallenge = {
  id: 'ch-1',
  connection_id: 'conn-power',
  method: 'sms',
  prompt: '',
  image: null,
  state: 'waiting',
  answered_by: null,
  raised_by: 'pull',
  created_at: '2026-09-17T09:01:00Z',
  expires_at: '2026-09-17T09:11:00Z',
  answered_at: null,
}

const provider: BillProviderInfo = {
  id: 'we-energies',
  name: 'Example Power',
  access: 'browser',
  sign_in: { kinds: ['live', 'typed'], prompt: '' },
  challenges: [],
  session_persists: true,
  keepalive_days: 0,
  reports_autopay: false,
  has_documents: true,
}

const agent: BillAgentStatus = {
  configured: true,
  healthy: true,
  error: '',
  providers: [provider],
}

describe('the challenge a connection is waiting on', () => {
  it('is the newest one still waiting, not the first in the list', () => {
    const older = { ...challenge, id: 'ch-old', created_at: '2026-09-16T09:00:00Z' }
    const found = waitingChallengeFor('conn-power', [older, challenge])
    expect(found?.id).toBe('ch-1')
  })

  it('ignores a challenge of another connection, and one already answered', () => {
    const elsewhere = { ...challenge, id: 'ch-other', connection_id: 'conn-waste' }
    const done = { ...challenge, id: 'ch-done', state: 'answered' as const }
    expect(waitingChallengeFor('conn-power', [elsewhere, done])).toBeNull()
  })
})

describe('the dialog a card opens', () => {
  it('answers the code request before offering a sign-in, session and all', () => {
    // The connection is signed in — the code is what stands between it and the
    // bills — so sending somebody to the sign-in would throw that session away.
    const stopped = { ...connection, last_pull_status: 'challenge' as const }
    expect(cardAction(stopped, [challenge])).toEqual({ action: 'challenge', challenge })
  })

  it('offers a sign-in for a challenge that expired before anybody answered', () => {
    const stopped = { ...connection, last_pull_status: 'challenge' as const }
    const gone = { ...challenge, state: 'expired' as const }
    expect(cardAction(stopped, [gone])).toEqual({ action: 'connect', challenge: null })
  })

  it('offers a sign-in to a connection that has none, or lost the one it had', () => {
    expect(cardAction({ ...connection, connected: false }, []).action).toBe('connect')
    expect(cardAction({ ...connection, needs_sign_in: true }, []).action).toBe('connect')
  })

  it('offers nothing to a connection that is simply working', () => {
    expect(cardAction(connection, [])).toEqual({ action: 'none', challenge: null })
  })
})

describe('the one button a card shows, and its menu', () => {
  it('updates a login that is working, and keeps signing in again in the menu', () => {
    const { primary, menu } = connectionActions(connection, [], true, true)
    expect(primary).toBe('update')
    expect(menu).toEqual(['connect', 'forget-session', 'match-history', 'edit', 'website', 'remove'])
  })

  it('puts the sign-in on the card when one is missing, and no update beside it', () => {
    const { primary, menu } = connectionActions({ ...connection, needs_sign_in: true }, [], true, true)
    expect(primary).toBe('connect')
    expect(menu).not.toContain('update')
    expect(menu).not.toContain('connect')
  })

  it('puts a waiting code request on the card, with the update and sign-in behind it', () => {
    const stopped = { ...connection, last_pull_status: 'challenge' as const }
    const { primary, menu } = connectionActions(stopped, [challenge], true, false)
    expect(primary).toBe('challenge')
    expect(menu.slice(0, 2)).toEqual(['update', 'connect'])
    expect(menu).not.toContain('website')
  })

  it('updates a lapsed session whose kept password has not been refused', () => {
    const lapsed = {
      ...connection,
      credential_source: 'stored' as const,
      needs_sign_in: true,
      last_pull_status: 'needs_sign_in' as const,
    }
    expect(connectionSignInNeed(lapsed)).toBe('password-signs-in')
    const { primary, menu } = connectionActions(lapsed, [], true, true)
    expect(primary).toBe('update')
    expect(menu[0]).toBe('connect')
  })

  it('asks for a sign-in once the kept password is refused, with one more try behind it', () => {
    const refused = {
      ...connection,
      credential_source: 'stored' as const,
      needs_sign_in: true,
      last_pull_status: 'needs_sign_in' as const,
      sign_in_paused: 'password_refused' as const,
    }
    expect(connectionSignInNeed(refused)).toBe('password-refused')
    const { primary, menu } = connectionActions(refused, [], true, true)
    expect(primary).toBe('connect')
    expect(menu[0]).toBe('update')
    expect(menu).toContain('forget-password')
  })

  it('asks for a sign-in once a code went unanswered, with one more try behind it', () => {
    // Both ways a code goes unanswered: a browser sign-in that got past the
    // password, and a parked challenge that expired.
    for (const stopped of [
      { needs_sign_in: true, last_pull_status: 'needs_sign_in' as const },
      { needs_sign_in: false, last_pull_status: 'challenge' as const },
    ]) {
      const waitingForYou = {
        ...connection,
        ...stopped,
        credential_source: 'stored' as const,
        sign_in_paused: 'code_needed' as const,
      }
      expect(connectionSignInNeed(waitingForYou)).toBe('code-needed')
      expect(cardAction(waitingForYou, []).action).toBe('connect')
      const { primary, menu } = connectionActions(waitingForYou, [], true, true)
      expect(primary).toBe('connect')
      expect(menu[0]).toBe('update')
    }
  })

  it('answers a live code request before anything a pause says', () => {
    const parked = {
      ...connection,
      last_pull_status: 'challenge' as const,
      sign_in_paused: 'code_needed' as const,
    }
    expect(connectionActions(parked, [challenge], true, true).primary).toBe('challenge')
  })

  it('asks for a sign-in when no password is kept', () => {
    const lapsed = { ...connection, needs_sign_in: true, last_pull_status: 'needs_sign_in' as const }
    expect(connectionSignInNeed(lapsed)).toBe('sign-in')
    expect(connectionSignInNeed(connection)).toBeNull()
  })

  it('offers to forget a kept password rather than the session under it', () => {
    const kept = { ...connection, credential_source: 'stored' as const }
    const { menu } = connectionActions(kept, [], true, true)
    expect(menu).toContain('forget-password')
    expect(menu).not.toContain('forget-session')
    expect(menuActionLabel('forget-password', kept, 'Example Power')).toBe('Forget password')
    expect(menuActionLabel('forget-password', { ...kept, has_totp: true }, 'Example Power')).toBe(
      'Forget password and key',
    )
  })

  it('offers no button at all for a provider nobody signs in to', () => {
    expect(connectionActions(connection, [], false, true)).toEqual({
      primary: null,
      menu: ['match-history', 'edit', 'website', 'remove'],
    })
  })

  it('lists removing last, where the menu draws it apart', () => {
    const { menu } = connectionActions(connection, [], true, true)
    expect(menu.at(-1)).toBe('remove')
    expect(menuActionLabel('website', connection, 'Example Power')).toBe('Example Power website')
  })
})

describe('why signing in is not on offer', () => {
  it('says nothing at all when it is', () => {
    expect(signInBlocked(agent, provider)).toBeNull()
  })

  it('does not call a deployment broken while it is still being asked', () => {
    expect(signInBlocked(undefined, null)).toContain('Checking')
  })

  it('separates a build with no engine from an engine that is not answering', () => {
    expect(signInBlocked({ ...agent, configured: false }, provider)).toContain(
      'carries no browser engine',
    )
    const down = signInBlocked({ ...agent, healthy: false, error: 'connection refused' }, provider)
    expect(down).toContain('not answering: connection refused')
  })

  it('says a provider whose bills arrive by email has nothing to sign in to', () => {
    const apple: BillProviderInfo = {
      ...provider,
      id: 'apple',
      name: 'Apple',
      access: 'email',
      sign_in: { kinds: [], prompt: '' },
      has_documents: false,
    }
    expect(signInBlocked({ ...agent, providers: [apple] }, apple)).toBe(
      'Apple has no sign-in: its bills arrive by email, and its card is for the ones you enter.',
    )
  })

  it('says when the agent on this server does not carry the provider', () => {
    expect(signInBlocked(agent, null)).toContain('does not carry this provider')
  })
})

describe('why a scheduled pull will not run', () => {
  it('says so for a provider that texts a code and has no hour to be pulled at', () => {
    const texts = { ...provider, challenges: ['sms' as const] }
    expect(scheduleNote(connection, texts)).toBe(
      'Updated only when you press Update now: Example Power asks for a text code, and nobody is awake to answer one at night.',
    )
  })

  it('says nothing once an hour is set, because then it is scheduled', () => {
    const texts = { ...provider, challenges: ['sms' as const] }
    expect(scheduleNote({ ...connection, pull_at: '18:30' }, texts)).toBeNull()
  })

  it('says nothing about a provider that asks for nothing, or a pull that is off', () => {
    expect(scheduleNote(connection, provider)).toBeNull()
    expect(scheduleNote({ ...connection, pull_enabled: false }, { ...provider, challenges: ['sms'] })).toBeNull()
  })

  it('names the tap for a provider that only wants one', () => {
    const taps = { ...provider, challenges: ['push' as const] }
    expect(scheduleNote(connection, taps)).toContain('asks for a tap on your phone')
  })
})
