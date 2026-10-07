import { readdirSync, readFileSync, statSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

/**
 * A table cell's second line must be `.cell__sub`, never a bare `.muted`.
 * `.muted` only sets a colour, so a muted span under a figure stays inline and
 * the two render as one string ("-$500.00$0.00 a year").
 *
 * Only the shape that breaks is flagged: a muted span that is a cell's direct
 * child following a `<Money>` or a plain `<span>`.
 */
const SRC = join(dirname(fileURLToPath(import.meta.url)), '..')

interface Tag {
  closing: boolean
  name: string
  classNames: string[]
  selfClosing: boolean
}

/**
 * The tags in a source file, hand-scanned because a JSX attribute can hold
 * both `>` and `"` (`showPlus={row.amount > 0}`), and a regex stopping at the
 * first `>` would flag nothing.
 */
function tags(source: string): Tag[] {
  const found: Tag[] = []

  for (let index = 0; index < source.length; index += 1) {
    if (source[index] !== '<') continue

    let at = index + 1
    const closing = source[at] === '/'
    if (closing) at += 1

    const name = /^[A-Za-z][\w.]*/.exec(source.slice(at))?.[0]
    if (name === undefined) continue
    at += name.length

    let depth = 0
    let quote: string | null = null
    let attrs = ''

    for (; at < source.length; at += 1) {
      const character = source[at]

      if (quote !== null) {
        if (character === quote) quote = null
      } else if (character === '"' || character === "'") {
        quote = character
      } else if (character === '{') {
        depth += 1
      } else if (character === '}') {
        depth -= 1
      } else if (character === '>' && depth === 0) {
        break
      }

      attrs += character
    }

    found.push({
      closing,
      name,
      classNames: (/className="([^"]*)"/.exec(attrs)?.[1] ?? '').split(/\s+/).filter(Boolean),
      selfClosing: attrs.trimEnd().endsWith('/'),
    })
    index = at
  }

  return found
}

function sourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return sourceFiles(path)
    return name.endsWith('.tsx') && !name.includes('.test.') ? [path] : []
  })
}

function offendersIn(source: string): string[] {
  const found: string[] = []
  let children: Tag[] | null = null
  let depth = 0

  for (const tag of tags(source)) {
    if (tag.name === 'Td' && !tag.closing && !tag.selfClosing) {
      children = []
      depth = 0
      continue
    }
    if (children === null) continue

    if (tag.name === 'Td' && tag.closing) {
      const runsTogether = children.some(
        (child, index) =>
          index > 0 &&
          child.name === 'span' &&
          child.classNames.includes('muted') &&
          (children![index - 1].name === 'Money' ||
            (children![index - 1].name === 'span' && children![index - 1].classNames.length === 0)),
      )
      if (runsTogether) found.push(children.map((child) => child.name).join(' + '))
      children = null
      continue
    }

    if (tag.closing) {
      depth -= 1
      continue
    }
    if (depth === 0) children.push(tag)
    if (!tag.selfClosing) depth += 1
  }

  return found
}

describe('a two-line table cell', () => {
  it('never leaves its second line inline', () => {
    const offenders = sourceFiles(SRC).flatMap((path) =>
      offendersIn(readFileSync(path, 'utf8')).map(
        (cell) => `${path.slice(SRC.length + 1)}: ${cell}`,
      ),
    )

    expect(offenders).toEqual([])
  })
})
