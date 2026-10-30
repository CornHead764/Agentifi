import { describe, expect, it } from 'vitest'

import { sendsOnEnter, type ComposerKey } from './composer'

const key = (over: Partial<ComposerKey> & { isComposing?: boolean; keyCode?: number }) => ({
  key: over.key ?? 'Enter',
  shiftKey: over.shiftKey ?? false,
  nativeEvent: { isComposing: over.isComposing ?? false, keyCode: over.keyCode ?? 13 },
})

describe('sendsOnEnter', () => {
  it('sends on a plain Enter', () => {
    expect(sendsOnEnter(key({}))).toBe(true)
  })

  it('does not send on Shift+Enter or another key', () => {
    expect(sendsOnEnter(key({ shiftKey: true }))).toBe(false)
    expect(sendsOnEnter(key({ key: 'a', keyCode: 65 }))).toBe(false)
  })

  it('does not send the Enter that confirms an input-method candidate', () => {
    expect(sendsOnEnter(key({ isComposing: true, keyCode: 229 }))).toBe(false)
    expect(sendsOnEnter(key({ isComposing: false, keyCode: 229 }))).toBe(false)
  })
})
