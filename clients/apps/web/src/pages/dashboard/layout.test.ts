import { describe, expect, it } from 'vitest'

import {
  DEFAULT_LAYOUT,
  WIDGET_IDS,
  moveWidget,
  parseLayout,
  setWidgetAccounts,
  toggleWidget,
  type WidgetLayout,
} from './layout'

function accountsOf(layout: readonly WidgetLayout[], id: string) {
  return layout.find((entry) => entry.id === id)?.accounts
}

describe('parseLayout', () => {
  it('falls back to the defaults for anything that is not a list', () => {
    // The stored value is JSON a person can edit in devtools, and an empty
    // dashboard is a worse answer than the built-in order.
    expect(parseLayout(null)).toEqual([...DEFAULT_LAYOUT])
    expect(parseLayout('recent')).toEqual([...DEFAULT_LAYOUT])
    expect(parseLayout({ recent: true })).toEqual([...DEFAULT_LAYOUT])
  })

  it('drops an unknown widget id and appends a missing one', () => {
    const parsed = parseLayout([{ id: 'gone', on: true }, { id: 'goals', on: false }])
    expect(parsed[0]).toEqual({ id: 'goals', on: false })
    expect(parsed.map((entry) => entry.id).sort()).toEqual([...WIDGET_IDS].sort())
  })

  it('keeps an account selection, strings only', () => {
    const parsed = parseLayout([{ id: 'recent', on: true, accounts: ['a', 7, null, 'b'] }])
    expect(accountsOf(parsed, 'recent')).toEqual(['a', 'b'])
  })

  it('leaves a widget that named no accounts without the key', () => {
    // Absent and empty mean opposite things: the default rule, and none.
    // Coercing one into the other would empty the widget for everybody who has
    // never opened Customize.
    expect(accountsOf(parseLayout([{ id: 'recent', on: true }]), 'recent')).toBeUndefined()
    expect(accountsOf(parseLayout([{ id: 'recent', on: true, accounts: 'all' }]), 'recent'))
      .toBeUndefined()
  })

  it('keeps an empty selection, which is a decision', () => {
    expect(accountsOf(parseLayout([{ id: 'recent', on: true, accounts: [] }]), 'recent'))
      .toEqual([])
  })
})

describe('editing a layout', () => {
  const chosen = setWidgetAccounts([...DEFAULT_LAYOUT], 'recent', ['a', 'b'])

  it('stores the accounts a widget was pointed at', () => {
    expect(accountsOf(chosen, 'recent')).toEqual(['a', 'b'])
  })

  it('hands the widget back to the default rule', () => {
    expect(accountsOf(setWidgetAccounts(chosen, 'recent', null), 'recent')).toBeUndefined()
  })

  it('leaves the selection alone when the widget is switched off and on', () => {
    const off = toggleWidget(chosen, 'recent')
    expect(off.find((entry) => entry.id === 'recent')?.on).toBe(false)
    expect(accountsOf(toggleWidget(off, 'recent'), 'recent')).toEqual(['a', 'b'])
  })

  it('leaves the selection alone when the widget is moved', () => {
    expect(accountsOf(moveWidget(chosen, 'recent', 1), 'recent')).toEqual(['a', 'b'])
  })

  it('survives a round trip through the stored JSON', () => {
    expect(accountsOf(parseLayout(JSON.parse(JSON.stringify(chosen))), 'recent'))
      .toEqual(['a', 'b'])
  })
})
