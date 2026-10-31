import { ESLint } from 'eslint'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const PACKAGE = fileURLToPath(new URL('..', import.meta.url))

describe('the core boundary', () => {
  it('refuses React, its renderers and DOM globals', async () => {
    // The fixture is ignored by the package's own lint run, so it is linted with ignores off.
    const eslint = new ESLint({ cwd: PACKAGE, ignore: false })
    const [result] = await eslint.lintFiles(['test/fixtures/breaksBoundary.ts'])
    const refusals = result.messages.map((message) => [message.ruleId, message.line])
    expect(refusals).toEqual([
      ['no-restricted-imports', 1],
      ['no-restricted-imports', 2],
      ['no-restricted-imports', 3],
      ['no-restricted-globals', 5],
      ['no-restricted-globals', 6],
    ])
  })
})
