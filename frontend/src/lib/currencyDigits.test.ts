import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import { currencyDigits } from './currencies'

/** The backend's committed ISO 4217 table, which `currencyDigits` mirrors. */
const ISO_TABLE = join(
  dirname(fileURLToPath(import.meta.url)),
  '../../../backend/internal/minorunits/iso4217.go',
)

describe('currencyDigits', () => {
  it('agrees with the backend on every currency with a minor unit', () => {
    const entries = [...readFileSync(ISO_TABLE, 'utf8').matchAll(/"([A-Z]{3})": (-?\d+),/g)]
    expect(entries.length).toBeGreaterThan(150)
    for (const [, code, digits] of entries) {
      // -1 is ISO's N.A.: the server refuses those currencies outright.
      if (digits === '-1') continue
      expect([code, currencyDigits(code)]).toEqual([code, Number(digits)])
    }
  })
})
