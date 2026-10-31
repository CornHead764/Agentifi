/**
 * The clean-up dialog's choices. The rule worth a test is the tree: the server
 * refuses a group whose subcategory stays, so the dialog must never send one.
 */

import { describe, expect, it } from 'vitest'

import type { UnusedList } from '@/lib/clients/categories'

import {
  checkedAgainst,
  chosenPhrase,
  counts,
  everything,
  purgeOutcome,
  purgeRequest,
  sectionState,
  setSection,
  toggle,
} from './cleanup'

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
    {
      id: 'lessons',
      parent_id: 'allowance',
      name: 'Lessons',
      kind: 'expense',
      path: 'Kids · Allowance · Lessons',
    },
    // A subcategory whose group is used, so the group is not on the list.
    {
      id: 'parking',
      parent_id: 'auto',
      name: 'Parking',
      kind: 'expense',
      path: 'Auto & Transport · Parking',
    },
  ],
  tags: [
    { id: 'someday', name: 'someday', color: null },
    { id: 'garage', name: 'garage', color: '#aa3300' },
  ],
  categories_checked: ['reviewed transactions', 'rule actions', 'subcategories'],
  tags_checked: ['reviewed transactions', 'goals'],
}

describe('what the dialog starts with', () => {
  it('ticks everything the server offered', () => {
    const all = everything(LIST)
    expect(all.size).toBe(6)
    expect(counts(LIST, all)).toEqual({
      categories: { chosen: 4, total: 4 },
      tags: { chosen: 2, total: 2 },
    })
  })
})

describe('keeping the tree whole', () => {
  it('unticking a subcategory keeps every group above it', () => {
    const next = toggle(LIST, everything(LIST), 'lessons')
    expect(next.has('lessons')).toBe(false)
    expect(next.has('allowance')).toBe(false)
    expect(next.has('kids')).toBe(false)
    // The unrelated branch and the tags are untouched.
    expect(next.has('parking')).toBe(true)
    expect(next.has('someday')).toBe(true)
  })

  it('ticking a group takes everything under it', () => {
    const none = setSection(LIST, everything(LIST), 'categories', false)
    const next = toggle(LIST, none, 'kids')
    expect(next.has('kids')).toBe(true)
    expect(next.has('allowance')).toBe(true)
    expect(next.has('lessons')).toBe(true)
    expect(next.has('parking')).toBe(false)
  })

  it('unticking a group leaves its subcategories to go on their own', () => {
    const next = toggle(LIST, everything(LIST), 'kids')
    expect(next.has('kids')).toBe(false)
    expect(next.has('allowance')).toBe(true)
  })

  it('a subcategory whose group is not listed stands alone', () => {
    const next = toggle(LIST, everything(LIST), 'parking')
    expect(next.has('parking')).toBe(false)
    expect(counts(LIST, next).categories.chosen).toBe(3)
  })

  it('a shift-click range takes the clicked row’s new state, tree rule and all', () => {
    const none = setSection(LIST, everything(LIST), 'categories', false)
    const ticked = toggle(LIST, none, 'parking', ['allowance', 'lessons', 'parking'])
    expect([...ticked].filter((id) => !['someday', 'garage'].includes(id)).sort()).toEqual([
      'allowance',
      'lessons',
      'parking',
    ])

    const unticked = toggle(LIST, everything(LIST), 'someday', ['someday', 'garage'])
    expect(counts(LIST, unticked).tags.chosen).toBe(0)
    expect(counts(LIST, unticked).categories.chosen).toBe(4)
  })

  it('a shift-click range clearing a subcategory clears the groups above it', () => {
    const next = toggle(LIST, everything(LIST), 'lessons', ['lessons', 'parking'])
    expect(next.has('kids')).toBe(false)
    expect(next.has('allowance')).toBe(false)
    expect(counts(LIST, next).categories.chosen).toBe(0)
  })

  it('a tag is just a tag', () => {
    const next = toggle(LIST, everything(LIST), 'garage')
    expect(next.has('garage')).toBe(false)
    expect(toggle(LIST, next, 'garage').has('garage')).toBe(true)
  })
})

describe('the section ticks', () => {
  it('clears or fills one kind and leaves the other', () => {
    const noTags = setSection(LIST, everything(LIST), 'tags', false)
    expect(counts(LIST, noTags)).toEqual({
      categories: { chosen: 4, total: 4 },
      tags: { chosen: 0, total: 2 },
    })
    expect(setSection(LIST, noTags, 'tags', true).size).toBe(6)
  })

  it('reads as all, none or some', () => {
    expect(sectionState({ chosen: 0, total: 3 })).toBe(false)
    expect(sectionState({ chosen: 3, total: 3 })).toBe(true)
    expect(sectionState({ chosen: 1, total: 3 })).toBe('indeterminate')
  })
})

describe('what the purge sends', () => {
  it('splits the ticks into categories and tags, in list order', () => {
    const next = toggle(LIST, everything(LIST), 'someday')
    expect(purgeRequest(LIST, next)).toEqual({
      category_ids: ['kids', 'allowance', 'lessons', 'parking'],
      tag_ids: ['garage'],
    })
  })

  it('sends nothing the list did not offer', () => {
    const stray = new Set(['kids', 'not-on-the-list'])
    expect(purgeRequest(LIST, stray)).toEqual({ category_ids: ['kids'], tag_ids: [] })
  })
})

describe('the words', () => {
  it('counts each kind and leaves out a kind with none', () => {
    expect(chosenPhrase({ categories: 3, tags: 1 })).toBe('3 categories and 1 tag')
    expect(chosenPhrase({ categories: 1, tags: 0 })).toBe('1 category')
    expect(chosenPhrase({ categories: 0, tags: 2 })).toBe('2 tags')
    expect(chosenPhrase({ categories: 0, tags: 0 })).toBe('nothing')
  })

  it("says what was checked, in the server's words", () => {
    expect(checkedAgainst(LIST.tags_checked)).toBe('Checked against reviewed transactions and goals.')
    expect(checkedAgainst([])).toBe('')
  })

  it('reports the filters a purge repaired or cleared', () => {
    expect(
      purgeOutcome({
        categories_deleted: 25,
        tags_deleted: 2,
        filters_repaired: 3,
        filters_retired: 1,
        resuggested: 0,
      }),
    ).toEqual({
      title: 'Deleted 25 categories and 2 tags',
      description:
        '3 saved filters no longer name them and 1 old register search that named only them was cleared.',
    })
    expect(
      purgeOutcome({
        categories_deleted: 1,
        tags_deleted: 0,
        filters_repaired: 0,
        filters_retired: 0,
        resuggested: 0,
      }),
    ).toEqual({ title: 'Deleted 1 category' })
  })

  it('says how many unreviewed rows are asked about again', () => {
    expect(
      purgeOutcome({
        categories_deleted: 1,
        tags_deleted: 0,
        filters_repaired: 0,
        filters_retired: 0,
        resuggested: 2,
      }).description,
    ).toBe(
      '2 unreviewed transactions an automation had filed under them are uncategorized and asked about again.',
    )
  })
})
