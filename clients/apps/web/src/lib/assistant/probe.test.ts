import { describe, expect, it } from 'vitest'

import { describeProbe } from './probe'

describe('the connection probe verdict', () => {
  const base = { reachable: true, native_tool_calls: false, prompted_tool_calls: false, detail: '', recommended: '' as const }

  it('names the setting to change when native calls are dropped and prompted ones work', () => {
    const verdict = describeProbe(
      { ...base, prompted_tool_calls: true, detail: 'The server is not turning the model’s tool calls into tool_calls.' },
      'native',
    )
    expect(verdict).toContain('drops native tool calls')
    expect(verdict).toContain('In the prompt')
  })

  it('says nothing needs changing when the saved style is the one that works', () => {
    expect(describeProbe({ ...base, native_tool_calls: true }, 'native')).toContain('Nothing to change')
    expect(describeProbe({ ...base, prompted_tool_calls: true, detail: 'ok' }, 'prompted')).toMatch(/^Reachable\./)
  })

  it('reports an unreachable model with the provider’s own words', () => {
    expect(describeProbe({ ...base, reachable: false, detail: 'the model refused (401): bad key' }, 'native')).toContain(
      'bad key',
    )
  })
})
