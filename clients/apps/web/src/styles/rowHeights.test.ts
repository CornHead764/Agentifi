import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import { remToPx } from '@/lib/scale'
import { NARROW_ROW_TWO_LINE, ROW_HEIGHT_TOKENS } from '@/lib/transactions/rows'

/**
 * The register's virtualizer places rows at the heights the row tokens give.
 * It reads them from the document, so a token it names that is missing or not
 * in rem would size every row to nothing; this reads them off disk instead.
 */
const tokens = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), 'tokens.css'),
  'utf8',
)

function token(name: string): string {
  const found = new RegExp(`^\\s*${name}:\\s*([^;]+);`, 'm').exec(tokens)
  return found ? found[1] : ''
}

describe('the row height tokens', () => {
  it('are defined in rem for every density the register offers', () => {
    for (const name of Object.values(ROW_HEIGHT_TOKENS)) {
      expect(remToPx(token(name), 16), name).toBeGreaterThan(0)
    }
  })

  it('grow from the dense row to the roomy one', () => {
    const [sm, md, lg] = (['sm', 'md', 'lg'] as const).map((density) =>
      remToPx(token(ROW_HEIGHT_TOKENS[density]), 16),
    )
    expect(sm).toBeLessThan(md)
    expect(md).toBeLessThan(lg)
  })

  it('leave a phone row two lines taller than the roomiest desktop row', () => {
    expect(NARROW_ROW_TWO_LINE).toBeGreaterThan(remToPx(token(ROW_HEIGHT_TOKENS.lg), 16))
  })
})

describe('remToPx', () => {
  it('reads a rem length at the root size given', () => {
    expect(remToPx(' 2.125rem', 16)).toBe(34)
    expect(remToPx('1.75rem', 20)).toBe(35)
  })

  it('reads nothing it cannot be sure of as zero', () => {
    expect(remToPx('', 16)).toBe(0)
    expect(remToPx('34px', 16)).toBe(0)
    expect(remToPx('var(--row-height)', 16)).toBe(0)
  })
})
