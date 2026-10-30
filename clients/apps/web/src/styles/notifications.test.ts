import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

/**
 * A connector's error can carry a whole URL, a single unbroken run of text that
 * would set the notifications panel's width. jsdom does no layout, so the rules
 * that prevent it are read off the stylesheet.
 */
const css = readFileSync(join(dirname(fileURLToPath(import.meta.url)), 'shell.css'), 'utf8')

function rule(selector: string): string {
  const escaped = selector.replace(/[.]/g, '\\.')
  return new RegExp(`(^|\\n)${escaped}\\s*\\{([^}]*)\\}`).exec(css)?.[2] ?? ''
}

describe('the notifications panel', () => {
  it('is bounded by the viewport and never scrolls sideways', () => {
    expect(rule('.notifications')).toMatch(/max-width:\s*calc\(100vw/)
    expect(rule('.notifications')).toMatch(/overflow-x:\s*hidden/)
    expect(rule('.notifications__list')).toMatch(/overflow-x:\s*hidden/)
  })

  it('breaks an unbroken run of text rather than widening for it', () => {
    expect(css).toMatch(
      /\.notifications__title,\s*\.notifications__text\s*\{[^}]*overflow-wrap:\s*anywhere/,
    )
    expect(rule('.notifications__content')).toMatch(/min-width:\s*0/)
  })
})
