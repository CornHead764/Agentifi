/**
 * The clean-up dialog's two lists, rendered without the dialog around them —
 * the dialog is a portal, which a static render does not draw.
 */

import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import type { UnusedList } from '@/lib/clients/categories'

import { CleanupChoices } from './CleanupUnusedDialog'
import { everything, toggle } from './cleanup'

const LIST: UnusedList = {
  categories: [
    { id: 'kids', parent_id: null, name: 'Kids', kind: 'expense', path: 'Kids' },
    {
      id: 'allowance',
      parent_id: 'kids',
      name: 'Allowance',
      kind: 'expense',
      path: 'Kids · Allowance',
    },
  ],
  tags: [{ id: 'someday', name: 'someday', color: null }],
  categories_checked: ['reviewed transactions', 'rule conditions'],
  tags_checked: ['reviewed transactions', 'goals'],
}

const choices = (list: UnusedList, selection = everything(list)) =>
  renderToStaticMarkup(
    <CleanupChoices list={list} selection={selection} onChange={() => undefined} />,
  )

describe('the clean-up choices', () => {
  it('lists each category by its branch and each tag by name, all ticked', () => {
    const markup = choices(LIST)
    expect(markup).toContain('Kids · Allowance')
    expect(markup).toContain('someday')
    expect(markup).toContain('Categories — 2 of 2')
    expect(markup).toContain('Tags — 1 of 1')
    expect(markup).not.toContain('data-state="unchecked"')
  })

  it('counts what was unticked', () => {
    const markup = choices(LIST, toggle(LIST, everything(LIST), 'allowance'))
    expect(markup).toContain('Categories — 0 of 2')
    expect(markup).toContain('Tags — 1 of 1')
  })

  it("says what each list was checked against, in the server's words", () => {
    const markup = choices(LIST)
    expect(markup).toContain('Checked against reviewed transactions and rule conditions.')
    expect(markup).toContain('Checked against reviewed transactions and goals.')
  })

  it('leaves out a kind with nothing unused', () => {
    const markup = choices({ ...LIST, tags: [] })
    expect(markup).toContain('Categories — 2 of 2')
    expect(markup).not.toContain('Tags —')
  })

  it('says so plainly when there is nothing to clean up', () => {
    const markup = choices({ ...LIST, categories: [], tags: [] })
    expect(markup).toContain('Nothing to clean up')
  })
})
