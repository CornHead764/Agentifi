import { describe, expect, it } from 'vitest'

import type { AssistantAction } from '@/lib/clients/assistant'
import { category } from '@/test/builders'

import {
  cardLink,
  cardNames,
  cardPhase,
  decidable,
  failureText,
  groupTally,
  ruleView,
  waiting,
} from './cards'

const HARBOR = 'aaaaaaaa-0000-0000-0000-000000000001'
const GROCERIES = 'cccccccc-0000-0000-0000-000000000001'

function action(overrides: Partial<AssistantAction> = {}): AssistantAction {
  return {
    id: '00000000-0000-0000-0000-0000000000a1',
    tool: 'create_rule',
    summary: 'File small Apple charges as groceries',
    method: 'POST',
    path: '/rules',
    body: null,
    status: 'pending',
    result: '',
    status_code: 0,
    created_at: '2026-08-01T10:00:01Z',
    decided_at: null,
    ...overrides,
  }
}

/** The body the server's create_rule builds for the request this feature was asked for. */
const APPLE_RULE = {
  name: 'Apple under $20 is Groceries',
  conditions: [
    {
      field: 'statement_name',
      operator: 'contains',
      group_index: 0,
      position: 0,
      negated: false,
      value_ids: [],
      value_texts: ['APPLE'],
    },
    {
      field: 'account',
      operator: 'in',
      group_index: 0,
      position: 1,
      negated: false,
      value_ids: [HARBOR],
      value_texts: [],
    },
    {
      field: 'amount',
      operator: 'less_than',
      group_index: 0,
      position: 2,
      negated: false,
      value_ids: [],
      value_texts: [],
      amount_min: null,
      amount_max: '20.00',
      state: false,
    },
  ],
  actions: { set_category_id: GROCERIES },
}

describe('the state a card is in', () => {
  it('names every status as the phase its badge shows', () => {
    expect(cardPhase('pending')).toBe('proposed')
    expect(cardPhase('applying')).toBe('running')
    expect(cardPhase('applied')).toBe('done')
    expect(cardPhase('failed')).toBe('failed')
    expect(cardPhase('discarded')).toBe('declined')
    expect(cardPhase('simulated')).toBe('simulated')
  })

  it('lets a waiting card and a refused one be accepted, and nothing else', () => {
    // A refusal changed nothing, so accepting again is a retry.
    expect(decidable({ status: 'pending' })).toBe(true)
    expect(decidable({ status: 'failed' })).toBe(true)
    for (const status of ['applying', 'applied', 'discarded', 'simulated'] as const) {
      expect(decidable({ status })).toBe(false)
    }
  })

  it('offers only waiting cards to Accept all, in the order they came', () => {
    const cards = [
      action({ id: 'one' }),
      action({ id: 'two', status: 'applied' }),
      action({ id: 'three' }),
      action({ id: 'four', status: 'failed' }),
    ]
    expect(waiting(cards).map((one) => one.id)).toEqual(['one', 'three'])
  })

  it('tallies a group by phase', () => {
    const tally = groupTally([
      { status: 'applied' },
      { status: 'applied' },
      { status: 'failed' },
      { status: 'pending' },
    ])
    expect(tally).toMatchObject({ done: 2, failed: 1, proposed: 1, running: 0 })
  })
})

describe('where a finished card leads', () => {
  it('links a made rule to that rule on the Rules page, and only once it is made', () => {
    expect(cardLink(action({ status: 'applied', resource_id: 'r1' }))).toEqual({
      to: '/rules?rule=r1',
      label: 'Open the rule',
    })
    expect(cardLink(action())).toBeNull()
    expect(cardLink(action({ status: 'failed' }))).toBeNull()
  })

  it('falls back to the list when the request named no row', () => {
    expect(cardLink(action({ status: 'applied' }))).toEqual({ to: '/rules', label: 'Open Rules' })
  })

  it('links a recurring item and a category to that row', () => {
    expect(
      cardLink(action({ path: '/series', status: 'applied', resource_id: 's1' })),
    ).toEqual({ to: '/upcoming/recurring?series=s1', label: 'Open the recurring item' })
    expect(
      cardLink(action({ path: '/categories/c1', method: 'PATCH', status: 'applied', resource_id: 'c1' })),
    ).toEqual({ to: '/settings/categories-tags?category=c1', label: 'Open the category' })
  })

  it('links a changed watchlist to that watchlist', () => {
    expect(
      cardLink(action({ path: '/watchlists/w1', method: 'PATCH', status: 'applied', resource_id: 'w1' })),
    ).toEqual({ to: '/watchlist/w1', label: 'Open the watchlist' })
  })

  it('links nowhere after a delete', () => {
    expect(cardLink(action({ path: '/rules/r1', method: 'DELETE', status: 'applied' }))).toBeNull()
  })
})

describe('why the app refused a card', () => {
  it('reads the sentence out of the API answer', () => {
    expect(failureText('{"detail":"name is required"}')).toBe('name is required')
  })

  it('reads a list of field errors as one line', () => {
    expect(failureText('{"detail":[{"msg":"bad amount"},{"msg":"bad date"}]}')).toBe(
      'bad amount; bad date',
    )
  })

  it('keeps a plain sentence and says something for an empty one', () => {
    expect(failureText('this depends on "Add Coffee"')).toBe('this depends on "Add Coffee"')
    expect(failureText('')).toBe('The app refused this change.')
  })
})

describe('a proposed rule, read the way the Rules page reads one', () => {
  const universe = { categories: [category(GROCERIES, 'Groceries')], tags: [] }

  it('names the account, the words and the amount, and what it files them under', () => {
    const names = cardNames(
      action({ preview: { resource: 'rules', verb: 'create', fields: [], names: { [HARBOR]: 'Harbor Cash Back' } } }),
      universe.categories,
    )
    const view = ruleView(action({ body: APPLE_RULE }), universe, names)
    expect(view).not.toBeNull()
    expect(view?.name).toBe('Apple under $20 is Groceries')
    expect(view?.conditions).toEqual([
      'Statement name contains APPLE',
      'Account: Harbor Cash Back',
      'expense under $20.00',
    ])
    expect(view?.then).toEqual(['Category → Groceries'])
  })

  it('falls back to a count when an account has no name to show', () => {
    const view = ruleView(action({ body: APPLE_RULE }), universe, new Map())
    expect(view?.conditions).toContain('1 account')
  })

  it('names a category a card beside it is about to create', () => {
    const body = { name: 'Coffee', conditions: APPLE_RULE.conditions.slice(0, 1), actions: { set_category_id: '@action:c1' } }
    const names = cardNames(
      action({ preview: { resource: 'rules', verb: 'create', fields: [], names: { '@action:c1': 'Coffee' } } }),
      [],
    )
    expect(ruleView(action({ body }), universe, names)?.then).toEqual(['Category → Coffee'])
  })

  it('is not a rule view for any other card', () => {
    expect(ruleView(action({ path: '/tags' }), universe, new Map())).toBeNull()
    expect(ruleView(action({ path: '/rules/r1', method: 'DELETE' }), universe, new Map())).toBeNull()
  })
})
