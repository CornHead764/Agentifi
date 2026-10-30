import { describe, expect, it, vi } from 'vitest'

import { openRowMenuAtPointer } from './rowContextMenu'

const COLLAPSED = { selectionIsCollapsed: () => true }

function click(inside: string | null) {
  const target = {
    closest: (selector: string) => (inside !== null && selector.includes(inside) ? {} : null),
  }
  return { target, clientX: 40, clientY: 90, preventDefault: vi.fn() }
}

describe('openRowMenuAtPointer', () => {
  it('opens the menu at the pointer and keeps the browser menu closed', () => {
    const event = click(null)
    const open = vi.fn()
    expect(openRowMenuAtPointer(event, open, COLLAPSED)).toBe(true)
    expect(event.preventDefault).toHaveBeenCalled()
    expect(open).toHaveBeenCalledWith({ x: 40, y: 90 })
  })

  it.each(['input', 'textarea', 'a'])('leaves the browser menu on %s', (tag) => {
    const event = click(tag)
    const open = vi.fn()
    expect(openRowMenuAtPointer(event, open, COLLAPSED)).toBe(false)
    expect(event.preventDefault).not.toHaveBeenCalled()
    expect(open).not.toHaveBeenCalled()
  })

  it('leaves the browser menu over selected text', () => {
    const event = click(null)
    const open = vi.fn()
    expect(openRowMenuAtPointer(event, open, { selectionIsCollapsed: () => false })).toBe(false)
    expect(event.preventDefault).not.toHaveBeenCalled()
    expect(open).not.toHaveBeenCalled()
  })
})
