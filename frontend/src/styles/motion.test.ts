import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const dir = dirname(fileURLToPath(import.meta.url))
const read = (file: string) =>
  readFileSync(join(dir, file), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '')
const sheets = readdirSync(dir)
  .filter((file) => file.endsWith('.css'))
  .map(read)

function reducedMotionBlock(css: string): string {
  const start = css.indexOf('@media (prefers-reduced-motion: reduce) {\n  *,')
  return start < 0 ? '' : css.slice(start, css.indexOf('\n}\n', start))
}

describe('reduced motion', () => {
  const base = reducedMotionBlock(read('base.css'))

  it('still stops decorative animation everywhere', () => {
    expect(base).toMatch(/animation-iteration-count:\s*1 !important/)
  })

  it('keeps the one spinner turning, slowly, rather than freezing it', () => {
    const spinners = sheets.flatMap((css) =>
      [
        ...css.matchAll(/(?:^|\n)([^{}\n@][^{}]*?)\s*\{[^}]*animation:\s*[\w-]*spin\s[^;]*infinite/g),
      ].map((match) => match[1].trim()),
    )
    expect(spinners).toEqual(['.spinner'])
    const exemption =
      /([^{}]+)\{\s*animation-duration:\s*2\.4s !important;\s*animation-iteration-count:\s*infinite !important;/.exec(
        base,
      )
    const exempted = (exemption?.[1] ?? '').split(',').map((selector) => selector.trim())
    expect(exempted).toEqual(['.spinner'])
  })
})

describe('overlays leaving', () => {
  const ui = read('ui.css')

  it('all fade out by the one animation, never too short to report its end', () => {
    const rules = [...ui.matchAll(/([^{}]+)\{[^}]*animation:\s*leave\s([^;]*);/g)]
    expect(rules).toHaveLength(1)
    const selectors = rules[0][1].split(',').map((selector) => selector.trim())
    expect(selectors).toEqual(
      ['overlay', 'dialog', 'sheet', 'menu', 'popover', 'tooltip', 'toast'].map(
        (name) => `.${name}[data-state='closed']`,
      ),
    )
    expect(rules[0][2]).toMatch(/^max\(var\(--motion\), 1ms\)/)
  })
})
