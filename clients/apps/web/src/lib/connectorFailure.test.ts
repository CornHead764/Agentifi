import { describe, expect, it } from 'vitest'

import { explainConnectorFailure } from './connectorFailure'

describe('a connector failure, for a person', () => {
  it.each([
    ['sign-in loop', 'kept returning to its sign-in form'],
    ['the sign-in form came back', 'kept returning to its sign-in form'],
    ['Costco answered the sign-in form with 403', 'refused the sign-in. Try again later'],
    ['unrecognised page at https://example.com/weird', 'does not recognise'],
    ['unrecognised factor page; offered voice, fax', 'cannot answer yet'],
    ['unanswerable state: approval_elsewhere', 'cannot answer yet'],
    ['there is no page to sign in on', 'sign-in page did not open'],
    ['context deadline exceeded', 'took too long'],
    ['dial tcp: lookup example.com: no such host', 'could not reach'],
  ])('turns %j into a next step, keeping the original as detail', (raw, said) => {
    const { message, detail } = explainConnectorFailure(raw, 'Example Co')
    expect(message).toContain(said)
    expect(message).not.toContain(raw)
    expect(detail).toBe(raw)
  })

  it("keeps a message the provider's own site wrote", () => {
    const raw = 'Amazon has no account for that email or number'
    expect(explainConnectorFailure(raw, 'Amazon')).toEqual({ message: `${raw}.`, detail: null })
  })

  it('folds an unknown engine error behind the details', () => {
    const raw = 'reading billing table: selector timed-in wrongly'
    const { message, detail } = explainConnectorFailure(raw, 'Example Co')
    expect(message).toContain('Something went wrong talking to Example Co')
    expect(detail).toBe(raw)
  })
})
