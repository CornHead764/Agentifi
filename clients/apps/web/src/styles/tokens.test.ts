import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { renderCss } from '@agentifi/tokens'
import { describe, expect, it } from 'vitest'

const TOKENS_CSS = join(dirname(fileURLToPath(import.meta.url)), 'tokens.css')

describe('tokens.css', () => {
  it('is generated from @agentifi/tokens', () => {
    const generated = renderCss()
    const message = 'edit clients/packages/tokens/tokens.json, then run `npm run tokens`'
    expect(generated, message).toBe(readFileSync(TOKENS_CSS, 'utf8'))
    expect(Buffer.from(generated).equals(readFileSync(TOKENS_CSS)), message).toBe(true)
  })
})
