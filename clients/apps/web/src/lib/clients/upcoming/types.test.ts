import { describe, expect, it } from 'vitest'

import { billLinkMark, type OccurrenceBillLink } from './types'

describe('the mark beside a linked reminder', () => {
  const name = (biller: string) => (biller === 'example-provider' ? 'Example Utility' : biller)
  const link = (health: OccurrenceBillLink['health']): OccurrenceBillLink => ({
    connection_id: 'c',
    biller: 'example-provider',
    connection_label: 'Main account',
    subaccount_label: 'Electric',
    health,
    autopay: true,
  })

  it('is nothing for a reminder the household keeps by hand', () => {
    expect(billLinkMark(null, name)).toBeNull()
  })

  it('reads linked while the provider answers, naming it', () => {
    expect(billLinkMark(link('ok'), name)).toEqual({ tone: 'linked', text: 'Kept current by Example Utility' })
  })

  it('reads broken, and says what to do, when the provider will not pull', () => {
    expect(billLinkMark(link('needs_sign_in'), name)?.tone).toBe('broken')
    expect(billLinkMark(link('not_connected'), name)?.text).toMatch(/not connected/)
    expect(billLinkMark(link('challenge'), name)?.text).toMatch(/code/)
    expect(billLinkMark(link('failed'), name)?.text).toMatch(/failed/)
  })
})
