import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { List, ListRow } from './ListRow'

describe('<ListRow>', () => {
  it('is one line with a title alone, and two with a second line', () => {
    expect(renderToStaticMarkup(<ListRow title="Checking" />)).not.toContain('list-row--two-line')
    expect(renderToStaticMarkup(<ListRow title="Checking" sub="Maple Bank" />)).toContain(
      'list-row--two-line',
    )
    expect(renderToStaticMarkup(<ListRow title="Coffee" figures="$4.00" figuresSub="Mar 3" />)).toContain(
      'list-row--two-line',
    )
  })

  it('puts actions in a RowActions, outside the pressable part', () => {
    const markup = renderToStaticMarkup(
      <ListRow title="Saved" onSelect={() => undefined} current actions={<button type="button">Delete</button>} />,
    )
    expect(markup).toContain('class="list-row__press" aria-current="true"')
    expect(markup).toMatch(/<\/button><span class="list-row__actions"><span class="row-actions"><button/)
  })

  it('renders no figures or actions track when it has none', () => {
    const markup = renderToStaticMarkup(<ListRow title="Plain" />)
    expect(markup).not.toContain('list-row__figures')
    expect(markup).not.toContain('list-row__actions')
    expect(markup).not.toContain('<button')
  })
})

describe('<List>', () => {
  it('rules lines between rows unless told not to', () => {
    expect(renderToStaticMarkup(<List />)).toContain('class="list list--dividers"')
    expect(renderToStaticMarkup(<List dividers={false} />)).toContain('class="list"')
  })
})
