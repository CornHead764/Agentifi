/**
 * The grid's two responsive contracts, which no visual test covers: the frame
 * that carries the edge shadow, and the per-cell column name a stacked row
 * prints. Also the order a sortable head reports.
 */

import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { SortableTh, Table, Td, Th } from './Table'

function markup(node: Parameters<typeof renderToStaticMarkup>[0]): string {
  return renderToStaticMarkup(node)
}

describe('the table', () => {
  it('wraps the scroller in a frame, which is what the edge shadow hangs off', () => {
    const rendered = markup(
      <Table>
        <tbody>
          <tr>
            <Td>One</Td>
          </tr>
        </tbody>
      </Table>,
    )

    expect(rendered).toContain('class="table-frame"')
    expect(rendered).toContain('class="table-scroll"')
  })

  it('stacks only when asked, so no table is restacked by accident', () => {
    const plain = markup(
      <Table>
        <tbody />
      </Table>,
    )
    const stacked = markup(
      <Table stack>
        <tbody />
      </Table>,
    )

    expect(plain).not.toContain('table--stack')
    expect(stacked).toContain('table--stack')
    // `table--stacked`, the state the stylesheet acts on, is added once the
    // width is known — never off a server render.
    expect(stacked).not.toContain('table--stacked')
  })

  it('gives a two-line table one row height, whatever density it was also given', () => {
    const twoLine = markup(
      <Table lines={2} density="sm">
        <tbody />
      </Table>,
    )

    expect(twoLine).toContain('table--two-line')
    expect(twoLine).not.toContain('table--sm')
  })

  it('marks a cell whose column name the caller chose, so nothing overwrites it', () => {
    const rendered = markup(
      <Table stack>
        <thead>
          <tr>
            <Th>Amount</Th>
            <Th>Account</Th>
          </tr>
        </thead>
        <tbody>
          <tr>
            <Td label="Email">on</Td>
            <Td>Everyday Checking</Td>
          </tr>
        </tbody>
      </Table>,
    )

    expect(rendered).toContain('data-label="Email"')
    expect(rendered).toContain('data-label-fixed=""')
    // The unlabelled cell takes its heading in the browser, so it carries no
    // label of its own here.
    expect(rendered).not.toContain('data-label="Account"')
  })

  it('lets a cell lead its row with no name at all', () => {
    const rendered = markup(
      <Table stack>
        <tbody>
          <tr>
            <Td label="">Netflix</Td>
          </tr>
        </tbody>
      </Table>,
    )

    expect(rendered).toContain('data-label=""')
    expect(rendered).toContain('data-label-fixed=""')
  })
})

describe('a sortable column head', () => {
  const noop = () => undefined

  it('tells a screen reader the order it sorts in', () => {
    const rendered = markup(<SortableTh label="Value" direction="desc" onSort={noop} numeric />)
    expect(rendered).toContain('aria-sort="descending"')
    expect(rendered).toContain('class="numeric"')
    expect(rendered).toContain('<svg')
  })

  it('draws no arrow and no order on a column that is not the sort', () => {
    const rendered = markup(<SortableTh label="Symbol" direction={null} onSort={noop} />)
    expect(rendered).not.toContain('aria-sort')
    expect(rendered).not.toContain('<svg')
    expect(rendered).toContain('Symbol')
  })
})
