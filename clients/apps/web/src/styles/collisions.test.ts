import { readdirSync, readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

/**
 * No two stylesheets may lay out the same class name. index.css loads every
 * sheet in one order, so a collision is decided by that order, which neither
 * rule shows when read on its own; jsdom does no layout to catch it.
 *
 * Only layout properties are checked: sharing a `display` is what moves things.
 */
const LAYOUT_PROPERTIES = new Set([
  'display',
  'grid-template-columns',
  'grid-template-rows',
  'grid-template-areas',
  'grid-column',
  'grid-row',
  'flex-direction',
  'position',
  'float',
  'width',
  'max-width',
])

/* Read off disk rather than imported: vitest stubs a CSS import to the empty
   string, `?raw` included, which makes a test built on one pass no matter what
   the stylesheets say. */
const STYLE_DIR = dirname(fileURLToPath(import.meta.url))

/** The class each rule actually applies to: the last compound in the selector. */
function subjectClasses(selector: string): string[] {
  const last = selector.trim().split(/[\s>+~]+/).pop() ?? ''
  return [...last.matchAll(/\.([A-Za-z0-9_-]+)/g)].map((match) => match[1])
}

function layoutClassesByFile(): Map<string, Set<string>> {
  const found = new Map<string, Set<string>>()

  for (const file of readdirSync(STYLE_DIR).filter((name) => name.endsWith('.css'))) {
    const css = readFileSync(join(STYLE_DIR, file), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '')

    for (const rule of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
      const selector = rule[1]
      if (selector.trim().startsWith('@')) continue

      const laysOut = rule[2]
        .split(';')
        .filter((declaration) => declaration.includes(':'))
        .some((declaration) => LAYOUT_PROPERTIES.has(declaration.split(':')[0].trim()))
      if (!laysOut) continue

      for (const part of selector.split(',')) {
        for (const cls of subjectClasses(part)) {
          const files = found.get(cls) ?? new Set<string>()
          files.add(file)
          found.set(cls, files)
        }
      }
    }
  }

  return found
}

describe('stylesheets', () => {
  it('never lay out the same class name from two files', () => {
    const collisions = [...layoutClassesByFile()]
      .filter(([, files]) => files.size > 1)
      .map(([cls, files]) => `.${cls} laid out by ${[...files].sort().join(' and ')}`)

    expect(collisions).toEqual([])
  })

  /**
   * `.money` may only size itself from inside a `:where()`. `<Money>` takes a
   * className, and at equal specificity base.css's `.money` would win over the
   * size class a page hands it; `:where(.money)` has no specificity at all.
   */
  it('never size .money outside a :where()', () => {
    const offenders: string[] = []

    for (const file of readdirSync(STYLE_DIR).filter((name) => name.endsWith('.css'))) {
      const css = readFileSync(join(STYLE_DIR, file), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '')

      for (const rule of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
        const sizes = rule[2]
          .split(';')
          .some((declaration) => declaration.split(':')[0].trim() === 'font-size')
        if (!sizes) continue

        for (const part of rule[1].split(',')) {
          if (!subjectClasses(part).includes('money')) continue
          if (/:where\([^)]*\.money/.test(part)) continue
          offenders.push(`${file}: ${part.trim()}`)
        }
      }
    }

    expect(offenders).toEqual([])
  })

  /**
   * A sideways scroller says what it does on the other axis. `overflow-x: auto`
   * alone computes `overflow-y` to `auto` too, and the pixel or two a one-row
   * strip overflows by swallows a vertical swipe on a phone. `hidden` for a
   * strip or a rail, `auto` where scrolling both ways is the point.
   */
  it('never scroll sideways without saying what the other axis does', () => {
    const offenders: string[] = []

    for (const file of readdirSync(STYLE_DIR).filter((name) => name.endsWith('.css'))) {
      const css = readFileSync(join(STYLE_DIR, file), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '')

      for (const rule of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
        const declarations = rule[2].split(';').map((one) => one.split(':').map((s) => s.trim()))
        const scrollsSideways = declarations.some(
          ([property, value]) => property === 'overflow-x' && (value === 'auto' || value === 'scroll'),
        )
        if (!scrollsSideways) continue
        if (declarations.some(([property]) => property === 'overflow-y')) continue
        offenders.push(`${file}: ${rule[1].trim()}`)
      }
    }

    expect(offenders).toEqual([])
  })

  /**
   * Every sheet is loaded by index.css, and no module imports one itself: a
   * sheet a page imports is only in the document once that page's chunk has
   * loaded, so a deep link to another page using its classes renders unstyled.
   */
  it('load every sheet from index.css and nowhere else', () => {
    const index = readFileSync(join(STYLE_DIR, 'index.css'), 'utf8')
    const imported = [...index.matchAll(/@import '\.\/([^']+)'/g)].map((m) => m[1])
    const sheets = readdirSync(STYLE_DIR).filter((name) => name.endsWith('.css') && name !== 'index.css')
    expect(sheets.filter((sheet) => !imported.includes(sheet))).toEqual([])

    const src = join(STYLE_DIR, '..')
    const importers = readdirSync(src, { recursive: true, encoding: 'utf8' })
      .filter((file) => /\.tsx?$/.test(file) && file !== 'main.tsx')
      .filter((file) => /^import ['"][^'"]+\.css['"]/m.test(readFileSync(join(src, file), 'utf8')))
    expect(importers).toEqual([])
  })
})
