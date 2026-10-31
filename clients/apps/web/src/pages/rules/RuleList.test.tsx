import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import type { QueryLike } from '@/components/QueryBoundary'

import { RuleList } from './RuleList'

function render(query: QueryLike<unknown>): string {
  return renderToStaticMarkup(
    <RuleList
      noun="rule"
      askKind="rule"
      thenHeading="Then"
      entries={[]}
      universe={{ categories: [], tags: [] }}
      query={query}
      empty={<p>No rules yet</p>}
      onMove={() => {}}
      onEdit={() => {}}
      onDelete={() => {}}
      onActiveChange={() => {}}
    />,
  )
}

describe('RuleList', () => {
  it('says the load failed rather than that there are no rules', () => {
    const html = render({
      data: undefined,
      isPending: false,
      error: new Error('boom'),
      refetch: () => {},
    })
    expect(html).toContain('Could not load this')
    expect(html).toContain('Try again')
    expect(html).not.toContain('No rules yet')
  })

  it('shows the empty state once an empty list has arrived', () => {
    const html = render({ data: [], isPending: false, error: null, refetch: () => {} })
    expect(html).toContain('No rules yet')
  })
})
