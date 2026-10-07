import { readdirSync, readFileSync } from 'node:fs'
import { dirname, extname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

/**
 * Every `var(--x)` names a property something sets. An undefined one is not an
 * error to the browser: the declaration silently falls back to its initial
 * value, so a mistyped token renders as black text, no gap or no border, and
 * nothing fails.
 *
 * A property counts as set when a stylesheet declares it or the code writes it
 * as a string key (`style={{ '--pop-x': … }}`, `setProperty('--depth', …)`).
 */

/* Set at runtime by a library rather than by this code. */
const RUNTIME_PREFIXES = ['--radix-']

const SRC_DIR = join(dirname(fileURLToPath(import.meta.url)), '..')

function sourceFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) return sourceFiles(path)
    return ['.css', '.ts', '.tsx'].includes(extname(entry.name)) && !entry.name.includes('.test.')
      ? [path]
      : []
  })
}

function scan() {
  const defined = new Set<string>()
  const used = new Map<string, Set<string>>()
  /* `var(--bucket-${key})`: a family, satisfied by any member. */
  const families = new Map<string, Set<string>>()

  for (const path of sourceFiles(SRC_DIR)) {
    const text = readFileSync(path, 'utf8').replace(/\/\*[\s\S]*?\*\//g, '')
    const file = relative(SRC_DIR, path)

    for (const match of text.matchAll(/(--[\w-]+)\s*:/g)) defined.add(match[1])
    for (const match of text.matchAll(/['"`](--[\w-]+)['"`]/g)) defined.add(match[1])

    for (const match of text.matchAll(/var\(\s*(--[\w-]+)(\$\{)?/g)) {
      const into = match[2] ? families : used
      const files = into.get(match[1]) ?? new Set<string>()
      files.add(file)
      into.set(match[1], files)
    }
  }
  return { defined, used, families }
}

describe('custom properties', () => {
  const { defined, used, families } = scan()
  const runtime = (name: string) => RUNTIME_PREFIXES.some((prefix) => name.startsWith(prefix))

  it('reads the sources it checks', () => {
    expect(used.size).toBeGreaterThan(100)
    expect(defined.has('--space-4')).toBe(true)
  })

  it('uses only properties that something sets', () => {
    const undefinedUses = [...used]
      .filter(([name]) => !defined.has(name) && !runtime(name))
      .map(([name, files]) => `${name} in ${[...files].join(', ')}`)
    expect(undefinedUses).toEqual([])
  })

  it('builds a property name only from a family that has members', () => {
    const empty = [...families]
      .filter(([prefix]) => ![...defined].some((name) => name.startsWith(prefix)))
      .map(([prefix, files]) => `${prefix}… in ${[...files].join(', ')}`)
    expect(empty).toEqual([])
  })
})
