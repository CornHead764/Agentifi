import { describe, expect, it } from 'vitest'

import type { LinkTarget, RemoteAccount } from '@/lib/clients/connections'

import {
  IGNORE,
  linkChoice,
  matchNote,
  NEW_ACCOUNT,
  offered,
  pairing,
  resolveChoices,
  usable,
} from './matchAccounts'

/**
 * The client's rules over the server's ranking: never offer an account that
 * already receives another feed, choose only a likely match, and say why
 * when nothing is chosen.
 */

function remote(over: Partial<RemoteAccount> = {}): RemoteAccount {
  return {
    external_id: 'acc-1',
    name: 'Bridge Checking',
    kind: 'cash',
    institution: 'Big Bank',
    masked_number: '0000',
    balance: 120_000 as RemoteAccount['balance'],
    currency: 'USD',
    linked_account_id: null,
    suggested: [],
    likely: null,
    match: 'none',
    ...over,
  }
}

function target(over: Partial<LinkTarget> = {}): LinkTarget {
  return {
    id: 'local-1',
    name: 'Everyday Checking',
    kind: 'cash',
    masked_number: '0000',
    balance: 120_000 as LinkTarget['balance'],
    sync_floor_on: '2026-08-15',
    linked_to: '',
    ...over,
  }
}

describe('which of the household’s accounts may be paired', () => {
  it('offers one that receives no feed', () => {
    expect(usable(target(), remote())).toBe(true)
  })

  it('withholds one already paired with a different provider account', () => {
    // One local account cannot receive two feeds.
    expect(usable(target({ linked_to: 'Savings at Big Bank' }), remote())).toBe(false)
  })

  it('keeps offering the account this remote one is already paired with', () => {
    // Otherwise the select would have no value to show for its own row.
    const already = target({ linked_to: 'Bridge Checking' })
    expect(usable(already, remote({ linked_account_id: already.id }))).toBe(true)
  })
})

describe('what an account pairs with before anybody chooses', () => {
  it('is the likely match', () => {
    const one = target()
    const against = remote({ suggested: [one.id], likely: one.id, match: 'number' })
    expect(pairing(against, [one])).toBe(one.id)
    expect(matchNote(against, [one])).toBe('Likely match: same account number')
  })

  it('is a new account when the suggestions tie, and says so', () => {
    const one = target()
    const two = target({ id: 'local-2' })
    const against = remote({ suggested: [one.id, two.id], match: 'tie' })
    expect(pairing(against, [one, two])).toBe(NEW_ACCOUNT)
    expect(matchNote(against, [one, two])).toBe('No clear match: more than one account fits as well')
  })

  it('is a new account when the suggestion is weak, and says so', () => {
    const one = target({ masked_number: '' })
    const against = remote({ suggested: [one.id], match: 'weak' })
    expect(pairing(against, [one])).toBe(NEW_ACCOUNT)
    expect(matchNote(against, [one])).toBe('No clear match: only the type, bank or balance is alike')
  })

  it('is a new account when the likely match already has another feed', () => {
    const taken = target({ linked_to: 'Savings at Big Bank' })
    const against = remote({ suggested: [taken.id], likely: taken.id, match: 'name' })
    expect(pairing(against, [taken])).toBe(NEW_ACCOUNT)
    expect(matchNote(against, [taken])).toBe('Its likely match is paired with another account')
  })

  it('is the pairing already made, over any guess', () => {
    const one = target()
    const two = target({ id: 'local-2', linked_to: 'Bridge Checking' })
    const against = remote({ linked_account_id: two.id, likely: one.id, match: 'number' })
    expect(pairing(against, [one, two])).toBe(two.id)
    expect(matchNote(against, [one, two])).toBe('Paired')
  })

  it('is a new account when nothing resembles it', () => {
    expect(pairing(remote(), [target({ kind: 'loan' })])).toBe(NEW_ACCOUNT)
    expect(matchNote(remote(), [])).toBe('Nothing here resembles it, so a new account is made')
  })
})

describe('an account somebody chose for', () => {
  it('says a new account is made, over the likely match', () => {
    const one = target()
    const against = remote({ suggested: [one.id], likely: one.id, match: 'number' })
    expect(matchNote(against, [one], NEW_ACCOUNT)).toBe('A new account is made when you finish')
  })

  it('says it is not imported, over the likely match', () => {
    const one = target()
    const against = remote({ suggested: [one.id], likely: one.id, match: 'number' })
    expect(matchNote(against, [one], IGNORE)).toBe('Not imported')
  })
})

describe('choices across the screen', () => {
  it('stops offering an account once another row has chosen it', () => {
    const one = target()
    const first = remote({ external_id: 'acc-1' })
    const second = remote({ external_id: 'acc-2' })
    const values = resolveChoices([first, second], [one], { 'acc-1': one.id })
    expect(offered(one, first, values)).toBe(true)
    expect(offered(one, second, values)).toBe(false)
  })

  it('offers it again once that row chooses something else', () => {
    const one = target()
    const first = remote({ external_id: 'acc-1' })
    const second = remote({ external_id: 'acc-2' })
    const values = resolveChoices([first, second], [one], { 'acc-1': IGNORE })
    expect(offered(one, second, values)).toBe(true)
  })

  it('makes a likely match another row chose into a new account, and says so', () => {
    const one = target()
    const chooser = remote({ external_id: 'acc-1' })
    const guesser = remote({ external_id: 'acc-2', suggested: [one.id], likely: one.id, match: 'number' })
    const values = resolveChoices([guesser, chooser], [one], { 'acc-1': one.id })
    expect(values).toEqual({ 'acc-1': one.id, 'acc-2': NEW_ACCOUNT })
    expect(matchNote(guesser, [one], undefined, values['acc-2'])).toBe(
      'Its likely match is chosen for another account',
    )
  })

  it('keeps every row that makes a new account', () => {
    const values = resolveChoices(
      [remote({ external_id: 'acc-1' }), remote({ external_id: 'acc-2' })],
      [],
      {},
    )
    expect(values).toEqual({ 'acc-1': NEW_ACCOUNT, 'acc-2': NEW_ACCOUNT })
  })
})

describe('what Finish sends', () => {
  it('pairs, creates or refuses, by what the row holds', () => {
    expect(linkChoice('acc-1', 'local-1')).toEqual({
      external_id: 'acc-1',
      action: 'link',
      account_id: 'local-1',
    })
    expect(linkChoice('acc-2', NEW_ACCOUNT)).toEqual({ external_id: 'acc-2', action: 'create' })
    expect(linkChoice('acc-3', IGNORE)).toEqual({ external_id: 'acc-3', action: 'ignore' })
  })
})
