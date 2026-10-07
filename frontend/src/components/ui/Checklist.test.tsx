import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { Checklist, SearchableChecklist } from './Checklist'

const noop = () => undefined

describe('the checklist', () => {
  it('ticks the chosen options', () => {
    const markup = renderToStaticMarkup(
      <Checklist
        options={[
          { id: 'a', label: 'Groceries' },
          { id: 'b', label: 'Travel' },
        ]}
        chosen={['b']}
        onChange={noop}
        empty="Nothing yet."
      />,
    )
    expect(markup.match(/aria-checked="true"/g)).toHaveLength(1)
    expect(markup.indexOf('aria-checked="true"')).toBeGreaterThan(markup.indexOf('Groceries'))
  })

  it('draws a heading over each group, with its options indented', () => {
    const markup = renderToStaticMarkup(
      <Checklist
        groups={[{ id: 'cash', label: 'Cash', options: [{ id: 'a', label: 'Everyday' }] }]}
        chosen={[]}
        onChange={noop}
        empty="Nothing yet."
      />,
    )
    expect(markup).toContain('option-list__heading eyebrow">Cash</p>')
    expect(markup).toContain('option-list__row option-list__row--child')
  })

  it('says so when there is nothing to tick', () => {
    const markup = renderToStaticMarkup(
      <Checklist groups={[]} chosen={[]} onChange={noop} empty="No open accounts." />,
    )
    expect(markup).toContain('<div class="empty empty--compact"><p class="empty__title">No open accounts.</p></div>')
  })

  it('ties a searchable list to its box for the arrow keys', () => {
    const markup = renderToStaticMarkup(
      <SearchableChecklist
        options={[{ id: 'a', label: 'Groceries', text: 'Groceries' }]}
        chosen={[]}
        onChange={noop}
        searchLabel="Search tags"
        empty="No matching tag."
      />,
    )
    const list = /data-arrow-list="([^"]+)"/.exec(markup)?.[1]
    expect(list).toBeDefined()
    expect(markup).toContain(`data-arrow-search="${list}"`)
    expect(markup).toContain('aria-label="Search tags"')
  })
})
