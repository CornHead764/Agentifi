import { describe, expect, it } from 'vitest'

import { openingTab, readTab, registerTabLabel, withTab } from './registerTab'

describe('the tab a register opens on', () => {
  it('is the rows for every account at once and for an account with no setting', () => {
    expect(openingTab(null)).toBe('all')
    expect(openingTab({ default_register_tab: null })).toBe('all')
  })

  it('is the account’s own setting', () => {
    expect(openingTab({ default_register_tab: 'spending' })).toBe('spending')
  })
})

describe('the tab on the URL', () => {
  it('outranks the account’s setting, the rows included', () => {
    expect(readTab(new URLSearchParams('tab=income'), 'spending')).toBe('income')
    expect(readTab(new URLSearchParams('tab=all'), 'spending')).toBe('all')
  })

  it('leaves the account’s setting in force when absent or unreadable', () => {
    expect(readTab(new URLSearchParams(''), 'spending')).toBe('spending')
    expect(readTab(new URLSearchParams('tab=budget'), 'income')).toBe('income')
  })

  it('is spelled out only where it differs from where the page opens', () => {
    expect(withTab(new URLSearchParams('displayNode=a1'), 'spending', 'spending').toString()).toBe(
      'displayNode=a1',
    )
    expect(withTab(new URLSearchParams('displayNode=a1'), 'all', 'spending').toString()).toBe(
      'displayNode=a1&tab=all',
    )
    expect(withTab(new URLSearchParams('tab=income'), 'all', 'all').toString()).toBe('')
  })

  it('names each tab as the tab bar does', () => {
    expect(registerTabLabel('income')).toBe('Income')
  })
})
