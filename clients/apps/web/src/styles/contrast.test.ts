import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

/**
 * Every colour text is written in reaches WCAG AA (4.5:1) on every surface it
 * can sit on, in both themes. Read off disk for the same reason as
 * `collisions.test.ts`: a CSS import is the empty string under vitest.
 */
const TOKENS = readFileSync(join(dirname(fileURLToPath(import.meta.url)), 'tokens.css'), 'utf8')

function block(selector: string): Record<string, string> {
  const start = TOKENS.indexOf(`${selector} {`)
  const body = TOKENS.slice(start, TOKENS.indexOf('\n}', start))
  const out: Record<string, string> = {}
  for (const m of body.matchAll(/(--[\w-]+):\s*(#[0-9a-fA-F]{6})\s*;/g)) out[m[1]] = m[2]
  return out
}

const dark = block(':root')
const light = { ...dark, ...block(':root[data-theme="light"]') }

function luminance(hex: string): number {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  })
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

function ratio(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}

const TEXT = [
  '--text',
  '--text-muted',
  '--text-faint',
  '--accent-fg',
  '--accent-fg-hover',
  '--income',
  '--expense',
  '--warning',
]
const SURFACES = ['--surface-0', '--surface-1', '--surface-2', '--surface-3', '--surface-hover']

describe.each([
  ['dark', dark],
  ['light', light],
])('%s theme', (_, tokens) => {
  it.each(TEXT)('%s reads at 4.5:1 on every surface', (fg) => {
    for (const bg of SURFACES) {
      expect(ratio(tokens[fg], tokens[bg]), `${fg} on ${bg}`).toBeGreaterThanOrEqual(4.5)
    }
  })

  it('button text reads on the accent fill', () => {
    expect(ratio(tokens['--accent-text'], tokens['--accent'])).toBeGreaterThanOrEqual(4.5)
  })
})
