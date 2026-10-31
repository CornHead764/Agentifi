/**
 * The Rules destination and its two halves: the tab is in the URL, the two
 * lists have the same columns, and an address that is neither half goes back
 * to the rules.
 */

import { Route, Routes } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { renderScreen } from '@/test/renderScreen'

import { RulesPage } from './RulesPage'

function rule(id: string, name: string) {
  return {
    id,
    name,
    filter_id: `f-${id}`,
    filter: {
      id: `f-${id}`,
      name: null,
      scope: 'rule',
      query_text: null,
      items: [
        {
          id: `i-${id}`,
          field: 'statement_name',
          operator: 'contains',
          group_index: 0,
          position: 0,
          negated: false,
          value_ids: [],
          value_texts: [name.toUpperCase()],
          text: null,
          amount_min: null,
          amount_max: null,
          date_from: null,
          date_to: null,
          date_preset: null,
          state: null,
        },
      ],
    },
    owns_filter: true,
    priority: 0,
    is_active: true,
    actions: {
      set_payee: null,
      set_category_id: null,
      add_tag_ids: [],
      set_notes: 'x',
      set_excluded_from_reports: null,
      set_excluded_from_spending_plan: null,
      set_is_reviewed: null,
    },
  }
}

function pageAt(path: string, rules?: unknown[]): string {
  return renderScreen(
    <Routes>
      <Route path="/rules" element={<RulesPage />} />
      <Route path="/rules/:tab" element={<RulesPage />} />
      <Route path="*" element={<p>elsewhere</p>} />
    </Routes>,
    { route: path, seed: rules ? [[['rules'], rules]] : [] },
  )
}

describe('<RulesPage>', () => {
  it('offers both halves from one destination', () => {
    const markup = pageAt('/rules')
    expect(markup).toContain('Rules')
    expect(markup).toContain('Guidance')
  })

  it('opens the half the address names, so a link can mean one of them', () => {
    expect(pageAt('/rules')).toContain('data-state="active"')
    // Radix marks the open panel; the guidance card is what is drawn under it.
    expect(pageAt('/rules/guidance')).toContain('Standing instructions for the assistant')
    expect(pageAt('/rules')).toContain('Run top to bottom on new transactions')
  })

  it('marks the one rule a link names, so an accepted card leads to it', () => {
    const markup = pageAt('/rules?rule=r2', [rule('r1', 'Cafe'), rule('r2', 'Market')])
    const marked = [...markup.matchAll(/<tr[^>]*data-linked="true"[^>]*>(.*?)<\/tr>/g)]
    expect(marked).toHaveLength(1)
    expect(marked[0][1]).toContain('Market')
    expect(marked[0][1]).not.toContain('Cafe')
  })

  it('marks nothing without a link', () => {
    expect(pageAt('/rules', [rule('r1', 'Cafe')])).not.toContain('data-linked')
  })

  it('draws neither half for an address that names neither', () => {
    // It redirects to /rules in a browser; what this render can say is that it
    // does not fall through to a tab strip over an empty panel.
    const markup = pageAt('/rules/nonsense')
    expect(markup).not.toContain('Run top to bottom on new transactions')
    expect(markup).not.toContain('Standing instructions for the assistant')
  })
})
