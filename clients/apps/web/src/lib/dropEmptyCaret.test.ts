import { describe, expect, it } from 'vitest'

import { keepsItsCaret, type CaretPress } from './dropEmptyCaret'

function press(over: Partial<CaretPress> = {}): CaretPress {
  return { shiftKey: false, inEditable: false, collapsed: true, hasRange: true, ...over }
}

describe('the caret a click leaves behind', () => {
  it('drops a selection that selected nothing', () => {
    expect(keepsItsCaret(press())).toBe(false)
  })

  it('keeps a real selection', () => {
    // Dragging, double-clicking and triple-clicking all land here.
    expect(keepsItsCaret(press({ collapsed: false }))).toBe(true)
  })

  it('keeps shift-click, which extends from a caret somebody placed', () => {
    expect(keepsItsCaret(press({ shiftKey: true }))).toBe(true)
  })

  it('keeps an editable field, where the caret is the point', () => {
    expect(keepsItsCaret(press({ inEditable: true }))).toBe(true)
  })

  it('has nothing to do when there is no selection at all', () => {
    expect(keepsItsCaret(press({ hasRange: false }))).toBe(true)
  })
})
