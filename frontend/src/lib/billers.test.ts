import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import {
  BILLERS,
  billerById,
  billerName,
  connectionName,
  connectionTitle,
  sortedByTitle,
} from './billers'

/** `domain.Billers`, which is the catalogue this table mirrors. */
const CATALOGUE = join(
  dirname(fileURLToPath(import.meta.url)),
  '../../../backend/internal/domain/bills.go',
)

function goSource(): string {
  return readFileSync(CATALOGUE, 'utf8')
}

/** Every `BillerID = "..."` constant the backend declares, read out of the Go source. */
function backendBillerIds(): string[] {
  return [...goSource().matchAll(/\bBillerID\s*=\s*"([^"]+)"/g)].map((found) => found[1])
}

/**
 * `domain.Billers` in declared order, which is the connect dialog's order. The
 * facts a screen reads (access path, portal link, statement fetch) are pinned too.
 */
function backendCatalogue(): {
  id: string
  name: string
  access: string
  home: string
  hasDocuments: boolean
  needsSite: boolean
  siteAddress: boolean
  medical: boolean
  generic: boolean
}[] {
  const source = goSource()
  const named = (pattern: RegExp) =>
    new Map([...source.matchAll(pattern)].map((found) => [found[1], found[2]]))
  const ids = named(/(\w+)\s+BillerID\s*=\s*"([^"]+)"/g)
  const accesses = named(/(\w+)\s+BillerAccess\s*=\s*"([^"]+)"/g)

  const table = source.slice(source.indexOf('var Billers = []Biller{'))
  const entries = [
    ...table.matchAll(/ID:\s*(\w+),\s*Name:\s*"([^"]+)",\s*Access:\s*(\w+),\s*Home:\s*"([^"]*)"([^}]*)/g),
  ]
  return entries.map((found) => ({
    id: ids.get(found[1]) ?? found[1],
    name: found[2],
    access: accesses.get(found[3]) ?? found[3],
    home: found[4],
    hasDocuments: /HasDocuments:\s*true/.test(found[5]),
    needsSite: /NeedsSite:\s*true/.test(found[5]),
    siteAddress: /SiteAddress:\s*true/.test(found[5]),
    medical: /Medical:\s*true/.test(found[5]),
    generic: /Generic:\s*true/.test(found[5]),
  }))
}

describe('the biller catalogue', () => {
  it('names the same providers the backend does', () => {
    const backend = backendBillerIds()
    // A parse that found nothing would pass every comparison below against an
    // empty table, so the count is asserted before the set is.
    expect(backend.length).toBe(BILLERS.length)
    expect(new Set(BILLERS.map((one) => one.id))).toEqual(new Set(backend))
  })

  it('lists them in the order the backend catalogue declares', () => {
    const backend = backendCatalogue()
    expect(backend.length).toBe(BILLERS.length)
    expect(BILLERS.map((one) => one.id)).toEqual(backend.map((one) => one.id))
  })

  it('agrees with the backend about how each provider is reached', () => {
    const backend = new Map(backendCatalogue().map((one) => [one.id, one]))
    for (const biller of BILLERS) {
      const theirs = backend.get(biller.id)
      expect(theirs, `${biller.id} is in the Go catalogue`).toBeDefined()
      expect({ access: biller.access, name: biller.name, hasDocuments: biller.hasDocuments }).toEqual({
        access: theirs?.access,
        name: theirs?.name,
        hasDocuments: theirs?.hasDocuments ?? false,
      })
    }
  })

  it('agrees about which providers are one deployment per customer, and how it is named', () => {
    const backend = new Map(backendCatalogue().map((one) => [one.id, one]))
    for (const biller of BILLERS) {
      expect(biller.site !== undefined, `${biller.id} needs a site`).toBe(backend.get(biller.id)?.needsSite)
      expect(biller.site?.address === true, `${biller.id} names it by address`).toBe(
        backend.get(biller.id)?.siteAddress,
      )
    }
  })

  it('agrees about which providers are medical receipts', () => {
    const backend = new Map(backendCatalogue().map((one) => [one.id, one.medical]))
    for (const biller of BILLERS) {
      expect(biller.medical === true, `${biller.id} is medical`).toBe(backend.get(biller.id))
    }
    expect(BILLERS.filter((one) => one.medical === true).map((one) => one.id)).toEqual(['mychart'])
  })

  it('agrees about which entry is no one company', () => {
    const backend = new Map(backendCatalogue().map((one) => [one.id, one.generic]))
    for (const biller of BILLERS) {
      expect(biller.generic === true, `${biller.id} is generic`).toBe(backend.get(biller.id))
    }
    expect(BILLERS.filter((one) => one.generic === true).map((one) => one.id)).toEqual(['email-only'])
  })

  it('sends a person to the same portal the backend names', () => {
    const backend = new Map(backendCatalogue().map((one) => [one.id, one.home]))
    for (const biller of BILLERS) {
      expect(biller.homeUrl, `${biller.id} home URL`).toBe(backend.get(biller.id))
    }
  })

  it('has one entry per id', () => {
    const ids = BILLERS.map((one) => one.id)
    expect(new Set(ids).size).toBe(ids.length)
  })

  it('names every provider and points at a real one', () => {
    for (const biller of BILLERS) {
      expect(biller.name).not.toBe('')
      // Empty (no portal) or https.
      if (biller.homeUrl !== '') expect(biller.homeUrl.startsWith('https://')).toBe(true)
    }
  })

  it('offers a statement only where a pull could fetch one', () => {
    // An email-only provider's document comes off the mail, never a fetch.
    for (const biller of BILLERS) {
      if (biller.access === 'email') {
        expect(biller.hasDocuments).toBe(false)
      }
    }
  })

  it('spells a provider name the way the provider spells it, not its id', () => {
    expect(billerName('alliant')).toBe('Alliant Energy')
    expect(billerName('we-energies')).toBe('We Energies')
  })

  it('falls back to the id for a provider this build has not heard of', () => {
    expect(billerById('nothing-like-it')).toBeUndefined()
    expect(billerName('nothing-like-it')).toBe('nothing-like-it')
  })
})

describe('what a connection is called', () => {
  it('is the provider when the connection has no name', () => {
    expect(connectionName({ biller: 'erie', label: '' })).toBe('Erie Insurance')
    expect(connectionTitle({ biller: 'erie', label: '  ' })).toBe('Erie Insurance')
  })

  it('is the company alone for the email-only provider, whose name is the company', () => {
    expect(connectionName({ biller: 'email-only', label: 'Example Water' })).toBe('Example Water')
    expect(connectionTitle({ biller: 'email-only', label: 'Example Water' })).toBe('Example Water')
  })

  it('is the name when it has one, with the provider in a heading', () => {
    expect(connectionName({ biller: 'community-connect', label: 'Water' })).toBe('Water')
    expect(connectionTitle({ biller: 'community-connect', label: 'Water' })).toBe(
      'Our Community Connect · Water',
    )
  })
})

describe('the connected providers', () => {
  it('are listed by their title, ignoring case, as the catalogue lists providers', () => {
    const rows = [
      { biller: 'spectrum', label: '' },
      { biller: 'email-only', label: 'water bill' },
      { biller: 'rsync-net', label: '' },
      { biller: 'apple', label: 'Phone' },
    ]
    expect(sortedByTitle(rows).map(connectionTitle)).toEqual([
      'Apple · Phone',
      'rsync.net',
      'Spectrum',
      'water bill',
    ])
    expect(rows[0].biller).toBe('spectrum')
  })
})
