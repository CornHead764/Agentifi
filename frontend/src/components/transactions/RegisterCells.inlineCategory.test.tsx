/**
 * Choosing a category in the list's category cell reviews the row, whether or
 * not the row had a suggestion waiting. The picker is replaced by a probe that
 * hands its `onChange` out, since the cells render to a string.
 */

import { describe, expect, it, vi } from 'vitest'

import { COLUMNS } from '@/lib/transactions/columns'
import type { Suggestion, Transaction, Uuid } from '@/lib/transactions/types'
import { transaction } from '@/test/builders'
import { renderScreen } from '@/test/renderScreen'
import { RegisterCell } from './RegisterCells'
import { RegisterContext, type RegisterView } from './register-context'

const pickers = vi.hoisted(() => ({ onChange: null as ((id: Uuid | null) => void) | null }))

vi.mock('./Pickers', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./Pickers')>()),
  CategoryPicker: (props: { onChange: (id: Uuid | null) => void }) => {
    pickers.onChange = props.onChange
    return null
  },
}))

const noop = () => undefined

function pick(row: Transaction, next: Uuid | null) {
  const edit = vi.fn()
  const applySuggestion = vi.fn()
  const view = {
    lookups: {
      accounts: [],
      categories: [],
      tags: [],
      accountName: () => 'Checking',
      categoryName: () => 'Groceries',
      tagName: () => 'tag',
      frequentCategoryIds: [],
    },
    actions: {
      edit,
      applySuggestion,
      setReviewed: noop,
      setTags: noop,
      openDetail: noop,
      discardSuggestion: noop,
      decidingSuggestion: false,
      refuse: noop,
      showRun: noop,
    },
    selection: {
      enabled: false,
      active: false,
      ids: new Set(),
      toggle: noop,
      begin: noop,
      visibleIds: [],
      toggleAll: noop,
    },
    sections: { collapsed: new Set(), toggle: noop },
    swipe: { left: 'menu', right: 'review' },
    multiAccount: true,
  } as RegisterView
  const column = COLUMNS.find((one) => one.id === 'category')!
  pickers.onChange = null
  renderScreen(
    <RegisterContext.Provider value={view}>
      <RegisterCell column={column} txn={row} />
    </RegisterContext.Provider>,
  )
  ;(pickers.onChange as ((id: Uuid | null) => void) | null)?.(next)
  return { edit, applySuggestion }
}

describe('changing a category from the list', () => {
  const next = 'c2' as Uuid

  it('reviews a row that had no suggestion', () => {
    const { edit } = pick(transaction({ category_id: 'c1' as Uuid, is_reviewed: false }), next)

    expect(edit).toHaveBeenCalledWith(
      expect.anything(),
      { category_id: next, is_reviewed: true },
      { category_id: next, is_reviewed: true },
    )
  })

  it('reviews a row that had a suggestion, through the suggestion path', () => {
    const suggestion = {
      action_id: 'a1',
      tool: 'update_transaction',
      category_id: 'c1' as Uuid,
      summary: '',
      splits: [],
      created_at: '2026-09-04T12:00:08Z',
    } as unknown as Suggestion
    const { edit, applySuggestion } = pick(transaction({ suggestion }), next)

    expect(applySuggestion).toHaveBeenCalledWith(expect.anything(), next)
    expect(edit).not.toHaveBeenCalled()
  })
})
