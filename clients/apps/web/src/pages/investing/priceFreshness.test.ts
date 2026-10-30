import { describe, expect, it } from 'vitest'

import { priceFreshness } from './priceFreshness'

const NOW = new Date('2026-09-13T12:00:00Z')

describe('how old the portfolio’s prices are', () => {
  it('says nothing when no security has ever been priced', () => {
    expect(priceFreshness([null, null], NOW)).toBeUndefined()
  })

  it('reads from the newest stamp, not the oldest', () => {
    // One security whose quote never arrived does not make the whole
    // portfolio stale, and the oldest stamp would say it did.
    const fresh = priceFreshness(['2024-01-01T00:00:00Z', '2026-09-13T09:00:00Z'], NOW)
    expect(fresh?.stale).toBe(false)
  })

  it('does not call a Friday close stale on the Monday after', () => {
    const friday = new Date('2026-09-11T20:00:00Z').toISOString()
    expect(priceFreshness([friday], new Date('2026-09-14T13:30:00Z'))?.stale).toBe(false)
  })

  it('calls two-week-old quotes stale', () => {
    expect(priceFreshness(['2026-08-30T05:18:38Z'], NOW)?.stale).toBe(true)
  })

  it('says how long ago in words rather than printing a locale timestamp', () => {
    const said = priceFreshness(['2026-08-30T05:18:38Z'], NOW)?.label ?? ''
    expect(said).toContain('Priced')
    expect(said).toContain('ago')
    expect(said).not.toContain('8/30/2026')
  })

  it('keeps the age on its own for a sentence to use', () => {
    const fresh = priceFreshness(['2026-08-30T05:18:38Z'], NOW)
    expect(fresh?.label).toBe(`Priced ${fresh?.since}`)
    expect(fresh?.since).toMatch(/ ago$/)
  })
})
