/** jsdom reports every element as zero wide, so the two lengths are passed in directly. */

import type { MouseEvent } from 'react'
import { describe, expect, it } from 'vitest'

import { titleWhenClipped } from './overflowTitle'

/** Just the surface the helper touches. */
function cell(text: string, scrollWidth: number, clientWidth: number) {
  const element = {
    textContent: text,
    scrollWidth,
    clientWidth,
    title: '',
    removeAttribute(name: string) {
      if (name === 'title') this.title = ''
    },
  }
  return element
}

function hover(element: ReturnType<typeof cell>): string {
  titleWhenClipped({ currentTarget: element } as unknown as MouseEvent<HTMLElement>)
  return element.title
}

describe('the title a clipped register cell grows on hover', () => {
  it('offers the whole text when the cell is showing less than all of it', () => {
    expect(hover(cell('Example Federal Home Loans', 248, 232))).toBe('Example Federal Home Loans')
  })

  it('stays away from a cell that already shows its text', () => {
    expect(hover(cell('Best Buy', 74, 232))).toBe('')
  })

  // A column that narrows and then widens again — the drawer opening, a column
  // switched on — must not keep a tooltip repeating what is back on screen.
  it('takes the title back off once the text fits again', () => {
    const element = cell('Example Federal Home Loans', 248, 232)
    expect(hover(element)).toBe('Example Federal Home Loans')

    element.clientWidth = 300
    expect(hover(element)).toBe('')
  })
})
