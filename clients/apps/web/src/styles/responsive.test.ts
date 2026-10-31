import { readdirSync, readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

/**
 * Layout rules that only break at phone and tablet widths. jsdom does no
 * layout, so they are pinned by what the stylesheet says.
 */

const STYLE_DIR = dirname(fileURLToPath(import.meta.url))

interface Rule {
  file: string
  /** The enclosing at-rule's prelude, e.g. `@media (max-width: 48rem)`. */
  within: string | null
  selectors: string[]
  declarations: Map<string, string>
}

/** Every style rule, one level of at-rule deep — which is all these sheets nest. */
function rules(): Rule[] {
  const found: Rule[] = []
  for (const file of readdirSync(STYLE_DIR).filter((name) => name.endsWith('.css'))) {
    const css = readFileSync(join(STYLE_DIR, file), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '')
    let depth = 0
    let within: string | null = null
    let start = 0
    for (let i = 0; i < css.length; i++) {
      const ch = css[i]
      if (ch === '{') {
        const prelude = css.slice(start, i).trim()
        if (prelude.startsWith('@')) {
          if (depth === 0) within = prelude
          depth++
          start = i + 1
          continue
        }
        const end = css.indexOf('}', i)
        const declarations = new Map<string, string>()
        for (const one of css.slice(i + 1, end).split(';')) {
          const colon = one.indexOf(':')
          if (colon > 0) declarations.set(one.slice(0, colon).trim(), one.slice(colon + 1).trim())
        }
        found.push({ file, within, selectors: prelude.split(',').map((s) => s.trim()), declarations })
        i = end
        start = end + 1
      } else if (ch === '}') {
        depth--
        if (depth === 0) within = null
        start = i + 1
      } else if (ch === ';' && depth >= 0) {
        // `@import` and the like end in a semicolon, not a block.
        if (css.slice(start, i).trim().startsWith('@')) start = i + 1
      }
    }
  }
  return found
}

const ALL = rules()

function rulesFor(selector: string): Rule[] {
  return ALL.filter((rule) => rule.selectors.includes(selector))
}

describe('responsive layout', () => {
  /* Safari can set the rail in a wider face than it was measured in, so a name
   * that does not fit takes a second line instead of an ellipsis. */
  it('never cuts a rail destination short', () => {
    const offenders = [...rulesFor('.rail__label'), ...rulesFor('.rail__link')]
      .filter(
        (rule) =>
          rule.declarations.get('text-overflow') === 'ellipsis' ||
          rule.declarations.get('white-space') === 'nowrap' ||
          rule.declarations.has('height'),
      )
      .map((rule) => `${rule.file}: ${rule.selectors.join(', ')}`)
    expect(offenders).toEqual([])
  })

  /*
   * Below 48rem the dashboard is stated as one track, and a widget spanning
   * two would make the grid invent a second. Anything that spans must come
   * back to one track there.
   */
  it('brings every multi-column span back to one track on a phone', () => {
    const spanning = ALL.filter(
      (rule) => rule.within === null && /^span [2-9]/.test(rule.declarations.get('grid-column') ?? ''),
    ).flatMap((rule) => rule.selectors)
    expect(spanning.length).toBeGreaterThan(0)

    const reset = new Set(
      ALL.filter(
        (rule) =>
          rule.within === '@media (max-width: 48rem)' &&
          /^(span 1|auto)$/.test(rule.declarations.get('grid-column') ?? ''),
      ).flatMap((rule) => rule.selectors),
    )
    expect(spanning.filter((selector) => !reset.has(selector))).toEqual([])
  })

  /*
   * The settings nav comes back beside the panels at 1025px, which a viewport
   * query cannot see, so every narrowing of the alerts is asked of the card,
   * and the narrowest tier's floor is one the card at that width can hold.
   */
  it('narrows the notification alerts on the card width, not the window', () => {
    const frame = rulesFor('.table-frame:has(> .table-scroll > .alerts)')
    expect(frame.map((rule) => rule.declarations.get('container'))).toEqual(['alerts / inline-size'])

    const narrowing = ALL.filter((rule) => rule.selectors.some((selector) => selector.startsWith('.alerts')))
      .filter((rule) => rule.within !== null && !rule.within.startsWith('@media (prefers'))
    expect(narrowing.length).toBeGreaterThan(0)
    for (const rule of narrowing) expect(rule.within).toMatch(/^@container alerts \(max-width: [\d.]+rem\)$/)

    for (const rule of rulesFor('.alerts')) {
      const floor = rule.declarations.get('min-width')
      if (!floor || rule.within === null) continue
      const query = Number(/max-width: ([\d.]+)rem/.exec(rule.within)?.[1])
      expect(parseFloat(floor)).toBeLessThan(query)
    }
  })

  /* A text input's automatic minimum width is its intrinsic size, wider than a
   * phone toolbar. */
  it('lets the register search box shrink below its input', () => {
    const base = rulesFor('.search').find((rule) => rule.within === null)
    expect(base?.declarations.get('min-width')).toBe('0')
  })

  /* A select beside a long hint must not shrink to its longest word. */
  it('keeps a setting row select at the width of its value', () => {
    const rule = rulesFor('.setting-row > .select__trigger').find((one) => one.within === null)
    expect(rule?.declarations.get('flex')).toBe('none')
    expect(rule?.declarations.get('white-space')).toBe('nowrap')
  })
})
