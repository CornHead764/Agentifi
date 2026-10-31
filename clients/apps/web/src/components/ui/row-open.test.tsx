import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { ConnectorRow } from '@/pages/settings/connector/ConnectorRow'

import { ListRow } from './ListRow'
import { opensRow, ROW_CONTROLS, rowOpenProps, type ClickNode } from './row-open'
import { Table, Td, Tr } from './Table'

/** A node that answers `closest` for the tags and roles it stands in for. */
function node(...matches: string[]): ClickNode {
  return {
    closest: (selector) =>
      matches.some((match) => selector.includes(match)) ? { matched: true } : null,
  }
}

const row = { contains: () => true }

describe('whether a click opens the row', () => {
  it('opens on a plain cell', () => {
    expect(opensRow({ row, target: node(), selecting: false })).toBe(true)
  })

  it.each([
    'a',
    'button',
    'input',
    'select',
    'textarea',
    '[role="button"]',
    '[role="switch"]',
    '[role="menuitem"]',
    '[data-row-click-ignore]',
  ])('leaves %s to answer its own click', (control) => {
    expect(ROW_CONTROLS).toContain(control)
    expect(opensRow({ row, target: node(control), selecting: false })).toBe(false)
  })

  it('leaves a drag-select across the row alone', () => {
    expect(opensRow({ row, target: node(), selecting: true })).toBe(false)
  })

  it('ignores a click that arrived from a portal outside the row', () => {
    expect(opensRow({ row: { contains: () => false }, target: node(), selecting: false })).toBe(
      false,
    )
  })

  it('ignores a target that is not an element', () => {
    expect(opensRow({ row, target: null, selecting: false })).toBe(false)
    expect(opensRow({ row, target: {}, selecting: false })).toBe(false)
  })
})

describe('the rows that take onOpen', () => {
  it('adds no handler and no marker without it', () => {
    expect(rowOpenProps(undefined)).toEqual({})
  })

  it('marks a table row, a list row and a connector row only when they open', () => {
    const table = (onOpen?: () => void) =>
      renderToStaticMarkup(
        <Table>
          <tbody>
            <Tr onOpen={onOpen}>
              <Td>Rent</Td>
            </Tr>
          </tbody>
        </Table>,
      )
    const list = (onOpen?: () => void) =>
      renderToStaticMarkup(
        <ul>
          <ListRow title="Rent" onOpen={onOpen} />
        </ul>,
      )
    const connector = (onOpen?: () => void) =>
      renderToStaticMarkup(
        <ul>
          <ConnectorRow title="Example Utility" actionsLabel="Actions" onOpen={onOpen} />
        </ul>,
      )

    for (const render of [table, list, connector]) {
      expect(render(() => {})).toContain('data-row-open')
      expect(render()).not.toContain('data-row-open')
    }
  })
})
