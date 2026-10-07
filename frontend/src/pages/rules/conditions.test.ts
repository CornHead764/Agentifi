import { describe, expect, it } from 'vitest'

import { EMPTY_UNIVERSE, type FilterUniverse } from '@/lib/transactions/filter'
import type { Category, Tag, Uuid } from '@/lib/transactions/types'

import {
  buildConditions,
  describeConditions,
  distinctKeywords,
  readConditions,
  seedConditions,
  seedKeywords,
} from './conditions'

describe('the keywords a rule opens on', () => {
  // The whole statement name would write a rule that matches the one charge it
  // came from, which is the opposite of what the action is for.
  it('drops the varying tail a bank appends', () => {
    expect(seedKeywords('SQ *HARBOR COFFEE 0000 SPRINGFIELD ZZ')).toEqual([
      'SQ',
      '*HARBOR',
      'COFFEE',
      'SPRINGFIELD',
      'ZZ',
    ])
  })

  it('leaves wording that carries no digits alone', () => {
    expect(seedKeywords('STREAMING.COM')).toEqual(['STREAMING.COM'])
  })

  it('collapses the whitespace a statement pads with', () => {
    expect(seedKeywords('  AMZN   MKTP  ')).toEqual(['AMZN', 'MKTP'])
  })

  // Better a keyword to correct than an empty column with no hint of what it
  // was going to say.
  it('keeps the wording when every token is tail', () => {
    expect(seedKeywords('4471 0099 12')).toEqual(['4471 0099 12'])
  })

  it('has nothing to say about a nameless charge', () => {
    expect(seedKeywords('   ')).toEqual([])
  })

  // A repeated word would seed two chips with the same key, and a list with
  // a repeated key stops reconciling: one × would delete both copies.
  it('seeds one chip for a word the bank says twice', () => {
    expect(seedKeywords('CHECKCARD 0912 AMAZON COM CHECKCARD WA')).toEqual([
      'CHECKCARD',
      'AMAZON',
      'COM',
      'WA',
    ])
  })
})

describe('what one keyword group holds', () => {
  it('keeps the order the words were added in', () => {
    expect(distinctKeywords(['GLACIER', 'FITNESS'])).toEqual(['GLACIER', 'FITNESS'])
  })

  it('takes a word once, wherever the repeat sits', () => {
    expect(distinctKeywords(['POS', 'DEBIT', 'POS'])).toEqual(['POS', 'DEBIT'])
  })

  // What the keyword box does on every commit: the typed word goes on the end,
  // and a word already there is not a second chip.
  it('appends a new word and refuses one already present', () => {
    expect(distinctKeywords([...['GLACIER'], 'FITNESS'])).toEqual(['GLACIER', 'FITNESS'])
    expect(distinctKeywords([...['GLACIER', 'FITNESS'], 'GLACIER'])).toEqual([
      'GLACIER',
      'FITNESS',
    ])
  })

  it('drops the padding and the blanks a typed word arrives with', () => {
    expect(distinctKeywords(['  FUEL  ', '', '   '])).toEqual(['FUEL'])
  })

  // Distinctness is what makes removal exact: the chips remove by value, so two
  // chips reading the same thing would be one ×.
  it('leaves a group where removing one word removes one criterion', () => {
    const group = seedKeywords('CHECKCARD AMAZON COM CHECKCARD WA')
    expect(group.filter((word) => word !== 'CHECKCARD')).toEqual(['AMAZON', 'COM', 'WA'])
  })
})

describe('opening the rule builder on a transaction', () => {
  const seed = {
    statement_name: 'GLACIER FITNESS 8821',
    payee: 'Glacier Fitness',
    category_id: 'c0ffee00-0000-4000-8000-000000000001',
  }

  it('matches on the bank wording, which survives the rule renaming the payee', () => {
    const draft = seedConditions(seed)
    expect(draft.nameField).toBe('statement_name')
    expect(draft.nameOperator).toBe('contains')
    expect(draft.keywordGroups).toEqual([['GLACIER', 'FITNESS']])
  })

  // A rule about a payee should fire wherever that payee is paid. Narrowing is
  // one click; widening is a condition the user has to go find.
  it('does not narrow to the account the charge landed on', () => {
    expect(seedConditions(seed).facets.accounts.ids).toEqual([])
    expect(seedConditions(seed).facets.amount).toBeNull()
  })

  it('produces conditions the builder will actually save', () => {
    const items = buildConditions(seedConditions(seed), EMPTY_UNIVERSE)
    expect(items).not.toBeNull()
    expect(items).toHaveLength(1)
    expect(items?.[0].field).toBe('statement_name')
    expect(items?.[0].value_texts).toEqual(['GLACIER', 'FITNESS'])
  })
})

/**
 * The facet half is the register's: what the builder saves is what
 * `toFilterItems` writes, and what it reopens with is what `fromFilterItems`
 * reads. These guard against a second encoder here.
 */
describe('the facets a rule or a note narrows by', () => {
  const groceries = { id: 'c-groceries' as Uuid, name: 'Groceries', parent_id: null }
  const universe: FilterUniverse = {
    categories: [groceries as unknown as Category],
    // Two, not one: choosing *every* tag is stored as the same item as "has
    // any tag", so a one-tag space cannot tell a tag selection from the facet.
    tags: [
      { id: 't-fun' as Uuid, name: 'Fun' } as unknown as Tag,
      { id: 't-work' as Uuid, name: 'Work' } as unknown as Tag,
    ],
  }

  const draft = (facets: Partial<(typeof EMPTY)['facets']>) => ({
    ...EMPTY,
    keywordGroups: [['FUEL'], ['GASMART']],
    facets: { ...EMPTY.facets, ...facets },
  })
  const EMPTY = seedConditions({ statement_name: '', payee: '', category_id: null })

  it('repeats every facet into every keyword alternative', () => {
    // A facet sitting alone in group 0 would be an alternative — "Fuel Stop, or
    // anything at all on this account" — rather than a condition on both.
    const items = buildConditions(
      draft({ accounts: { ids: ['a-1' as Uuid], negated: false } }),
      universe,
    )
    expect(items).not.toBeNull()
    const accountGroups = (items ?? [])
      .filter((item) => item.field === 'account')
      .map((item) => item.group_index)
    expect(accountGroups).toEqual([0, 1])
  })

  it('names the attachment question as a chip, either way round', () => {
    expect(describeConditions(draft({ hasAttachment: true }))).toContain('Has attachments')
    expect(describeConditions(draft({ hasAttachment: false }))).toContain('No attachments')
    expect(describeConditions(draft({})).join(' ')).not.toContain('attachments')
    expect(describeConditions(draft({ missingReceipt: true }))).toContain('Missing a receipt')
  })

  it('round trips categories, tags and an amount through the stored items', () => {
    const before = draft({
      categories: { ids: [groceries.id], negated: false },
      tags: { ids: ['t-fun' as Uuid], negated: true },
      amount: { min: '20.00', max: null, direction: 'expense', operator: 'equals' },
      isReviewed: false,
    })
    const items = buildConditions(before, universe)
    expect(items).not.toBeNull()

    const after = readConditions(items ?? [], universe)
    expect(after.keywordGroups).toEqual(before.keywordGroups)
    expect(after.facets.categories).toEqual(before.facets.categories)
    expect(after.facets.tags).toEqual(before.facets.tags)
    expect(after.facets.amount).toEqual(before.facets.amount)
    expect(after.facets.isReviewed).toBe(false)
  })

  // A rule saved before the seed deduplicated, or one written by hand, reopens
  // as the same unreconcilable chip list unless it is cleaned on the way in.
  it('reopens a stored group with one chip per word', () => {
    const stored = buildConditions({ ...EMPTY, keywordGroups: [['POS', 'DEBIT']] }, universe) ?? []
    const repeated = stored.map((item) => ({ ...item, value_texts: ['POS', 'DEBIT', 'POS'] }))
    expect(readConditions(repeated, universe).keywordGroups).toEqual([['POS', 'DEBIT']])
  })

  it('reads the facets from one group rather than unioning them all', () => {
    // They are written into every group, so reading them all would union a set
    // with itself — and on a hand-written filter whose groups differ, widen it.
    const items = buildConditions(
      draft({ accounts: { ids: ['a-1' as Uuid], negated: false } }),
      universe,
    )
    expect(readConditions(items ?? [], universe).facets.accounts.ids).toEqual(['a-1'])
  })

  it('leaves no facet out of the summary a rule row prints', () => {
    // A condition missing from the chips reads as a rule that fires more widely
    // than it does, which is the one way this summary can be wrong.
    const chips = describeConditions(
      draft({
        categories: { ids: [groceries.id], negated: false },
        uncategorized: true,
        tags: { ids: ['t-fun' as Uuid], negated: false },
        accounts: { ids: ['a-1' as Uuid], negated: false },
        flags: { values: ['flagged'], negated: false },
        amount: { min: '20.00', max: null, direction: 'expense', operator: 'greater_than' },
        isReviewed: true,
        isPending: false,
      }),
    )
    expect(chips).toEqual([
      'Statement name contains FUEL',
      'Statement name contains GASMART',
      '1 category',
      'Uncategorized',
      '1 tag',
      '1 account',
      'Flagged',
      'expense over $20.00',
      'Reviewed',
      'Settled',
    ])
  })

  it('prints the amount bounds as money and skips one that is not an amount', () => {
    const chips = (amount: (typeof EMPTY)['facets']['amount']) =>
      describeConditions({ ...draft({ amount }), keywordGroups: [] })
    expect(chips({ min: '10', max: '1250.5', direction: 'income' })).toEqual([
      'income between $10.00 and $1,250.50',
    ])
    expect(chips({ min: 'lots', max: null, operator: 'greater_than' })).toEqual([])
  })
})
