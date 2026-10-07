/**
 * A proposed change's card in each of its states: which buttons it offers
 * when. A card offering Accept while running can be accepted twice.
 */

import type { ReactNode } from 'react'
import { describe, expect, it } from 'vitest'

import type { AssistantAction } from '@/lib/clients/assistant'
import { ACCOUNTS_KEY, CATEGORIES_KEY, TAGS_KEY } from '@/lib/transactions/cache'
import type { Category } from '@/lib/transactions/types'
import { renderScreen } from '@/test/renderScreen'

import { ActionCard, ActionGroupCard } from './ActionCards'

const CONVERSATION = '00000000-0000-0000-0000-0000000000c1'
const HARBOR = 'aaaaaaaa-0000-0000-0000-000000000001'
const GROCERIES = 'cccccccc-0000-0000-0000-000000000001'

const GROCERIES_ROW: Category = {
  id: GROCERIES,
  parent_id: null,
  name: 'Groceries',
  kind: 'expense',
  known_category_id: null,
  txf_id: null,
  txf_ids: [],
  is_user_assignable: true,
  is_editable: true,
  protected_reason: null,
  excluded_from_reports: false,
  excluded_from_spending_plan: false,
  excluded_from_category_list: false,
  sort_order: 0,
}

function rule(overrides: Partial<AssistantAction> = {}): AssistantAction {
  return {
    id: '00000000-0000-0000-0000-0000000000a1',
    tool: 'create_rule',
    summary: 'File small Apple charges on the Harbor card as groceries',
    method: 'POST',
    path: '/rules',
    body: {
      name: 'Apple under $20 is Groceries',
      conditions: [
        { field: 'statement_name', operator: 'contains', group_index: 0, value_texts: ['APPLE'] },
        { field: 'account', operator: 'in', group_index: 0, value_ids: [HARBOR] },
        { field: 'amount', operator: 'less_than', group_index: 0, amount_max: '20.00', state: false },
      ],
      actions: { set_category_id: GROCERIES },
    },
    status: 'pending',
    result: '',
    status_code: 0,
    created_at: '2026-08-01T10:00:01Z',
    decided_at: null,
    preview: {
      resource: 'rules',
      verb: 'create',
      fields: [
        { label: 'Name', value: 'Apple under $20 is Groceries', kind: 'text' },
        { label: 'Then category', value: 'Groceries', kind: 'name' },
      ],
      names: { [HARBOR]: 'Harbor Cash Back', [GROCERIES]: 'Groceries' },
    },
    ...overrides,
  }
}

function render(node: ReactNode): string {
  return renderScreen(node, {
    seed: [
      [CATEGORIES_KEY, [GROCERIES_ROW]],
      [TAGS_KEY, []],
      [ACCOUNTS_KEY, []],
    ],
  })
}

function card(action: AssistantAction): string {
  return render(<ActionCard action={action} conversationId={CONVERSATION} />)
}

/** The labels of every button in the markup, as a person reads them. */
function buttons(markup: string): string[] {
  return [...markup.matchAll(/<button[^>]*>(.*?)<\/button>/g)].map((match) =>
    match[1].replace(/<[^>]+>/g, '').trim(),
  )
}

describe('a proposed rule', () => {
  it('reads as the Rules page reads a rule, with Accept and Decline', () => {
    const markup = card(rule())
    expect(markup).toContain('Apple under $20 is Groceries')
    expect(markup).toContain('If a transaction matches')
    expect(markup).toContain('Statement name contains APPLE')
    expect(markup).toContain('Account: Harbor Cash Back')
    expect(markup).toContain('expense under $20.00')
    expect(markup).toContain('Category → Groceries')
    expect(markup).toContain('Proposed')
    expect(buttons(markup)).toEqual(['Accept', 'Decline'])
  })

  it('keeps the request it will send, folded, beneath the words', () => {
    const markup = card(rule())
    expect(markup).toContain('The request it will send')
    expect(markup).toContain('POST')
    expect(markup).toContain('/rules')
  })
})

describe('a card as it moves', () => {
  it('offers nothing to press while its request is running', () => {
    const markup = card(rule({ status: 'applying' }))
    expect(markup).toContain('Applying…')
    expect(buttons(markup)).toEqual([])
  })

  it('says it is done and links to what it made', () => {
    const markup = card(
      rule({ status: 'applied', status_code: 201, resource_id: 'bbbbbbbb-0000-0000-0000-000000000001' }),
    )
    expect(markup).toContain('Done')
    expect(markup).toContain('href="/rules?rule=bbbbbbbb-0000-0000-0000-000000000001"')
    expect(markup).toContain('Open the rule')
    expect(buttons(markup)).not.toContain('Decline')
  })

  it('says why the app refused it, and offers to try again or decline', () => {
    const markup = card(
      rule({ status: 'failed', status_code: 409, result: '{"detail":"a rule needs at least one condition"}' }),
    )
    expect(markup).toContain('Failed')
    expect(markup).toContain('a rule needs at least one condition')
    expect(markup).not.toContain('{&quot;detail&quot;')
    expect(markup).toContain('Try again')
    expect(markup).toContain('Decline')
  })

  it('keeps a declined card in the thread with the reason given', () => {
    const markup = card(rule({ status: 'discarded', decline_reason: 'Use Dining, not Groceries' }))
    expect(markup).toContain('Declined')
    expect(markup).toContain('Use Dining, not Groceries')
    expect(markup).not.toContain('Try again')
    expect(buttons(markup)).not.toContain('Decline')
  })
})

describe('a card over an endpoint with no view of its own', () => {
  it('reads out the fields the server named', () => {
    const markup = card(
      rule({
        tool: 'update_watchlist',
        summary: 'Lower the grocery watchlist',
        method: 'PATCH',
        path: '/watchlists/w1',
        body: { target_amount: '250.00' },
        preview: {
          resource: 'watchlists',
          verb: 'update',
          fields: [{ label: 'Target', value: '250.00', kind: 'money' }],
          names: {},
        },
      }),
    )
    expect(markup).toContain('Target')
    expect(markup).toContain('250.00')
    expect(markup).not.toContain('If a transaction matches')
  })
})

describe('a bulk proposal', () => {
  function row(id: string, status: AssistantAction['status'], summary: string): AssistantAction {
    return rule({
      id,
      tool: 'update_transactions',
      summary,
      method: 'PATCH',
      path: `/transactions/${id}`,
      body: { category_id: GROCERIES },
      group_id: 'g1',
      status,
      preview: {
        resource: 'transactions',
        verb: 'update',
        fields: [{ label: 'Category', value: 'Groceries', kind: 'name' }],
        names: { [GROCERIES]: 'Groceries' },
        group_summary: 'File the market runs as groceries',
      },
    })
  }

  it('is one card with every row and one Accept for all of them', () => {
    const markup = render(
      <ActionGroupCard
        actions={[
          row('t1', 'pending', '2026-08-02 · Market · -12.00'),
          row('t2', 'pending', '2026-08-09 · Market · -18.50'),
        ]}
        conversationId={CONVERSATION}
      />,
    )
    expect(markup).toContain('File the market runs as groceries')
    expect(markup).toContain('2 changes')
    expect(markup).toContain('2026-08-09 · Market · -18.50')
    expect(markup).toContain('Accept all 2')
    expect(markup).toContain('Decline all')
  })

  it('says how the rows went once some are decided, and offers only what is left', () => {
    const markup = render(
      <ActionGroupCard
        actions={[
          row('t1', 'applied', 'first'),
          row('t2', 'failed', 'second'),
          row('t3', 'pending', 'third'),
        ]}
        conversationId={CONVERSATION}
      />,
    )
    expect(markup).toContain('1 waiting · 1 applied · 1 refused')
    expect(markup).toContain('Accept all 2')
  })

  it('offers nothing once every row is decided', () => {
    const markup = render(
      <ActionGroupCard
        actions={[row('t1', 'applied', 'first'), row('t2', 'discarded', 'second')]}
        conversationId={CONVERSATION}
      />,
    )
    expect(markup).toContain('1 applied · 1 declined')
    expect(markup).not.toContain('Accept all')
  })
})
