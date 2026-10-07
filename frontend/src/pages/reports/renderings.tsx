/**
 * The two renderings. Both take one `ReportResult` and add nothing up: every
 * figure came from the engine's one grouping pass, and a component that summed
 * a column would be a second implementation of the number. Which renders is
 * decided by which half of the result is populated, which `config.mode`
 * decides.
 */

import { Fragment } from 'react'

import { Money } from '@/components/Money'
import { Table, TableEmptyRow, Td, Th } from '@/components/ui'
import type {
  ReportNode,
  ReportSummaryResult,
  ReportTransactionResult,
  TimeGrain,
} from '@/lib/clients/reports'
import { depthStyle } from '@/lib/depthStyle'
import { formatDate } from '@/lib/format'
import { columnHeading } from '@/lib/reports/labels'

/** Ids to names, for the two columns the engine returns as ids. */
export interface NameLookup {
  account: (id: string) => string
  category: (id: string | null) => string
}

export function TransactionReport({
  result,
  heading,
  names,
}: {
  result: ReportTransactionResult
  /** The first column's header — the row dimension this report groups by. */
  heading: string
  names: NameLookup
}) {
  if (result.count === 0) {
    return (
      <Table density="sm">
        <tbody>
          <TableEmptyRow colSpan={5}>No transactions fall inside this report.</TableEmptyRow>
        </tbody>
      </Table>
    )
  }

  return (
    <Table density="sm">
      <thead>
        <tr>
          <Th>{heading}</Th>
          <Th>Date</Th>
          <Th>Account</Th>
          <Th>Category</Th>
          <Th numeric>Amount</Th>
        </tr>
      </thead>
      <tbody>
        {result.groups.map((group) => (
          <GroupRows key={group.key} node={group} trail="" names={names} />
        ))}
        <tr className="report__grand">
          <Td colSpan={4}>Grand total</Td>
          <Td numeric>
            <Money value={result.total} tone="neutral" />
          </Td>
        </tr>
      </tbody>
    </Table>
  )
}

function GroupRows({
  node,
  trail,
  names,
}: {
  node: ReportNode
  trail: string
  names: NameLookup
}) {
  const path = `${trail}/${node.key}`

  return (
    <>
      <tr className="report__group">
        <Td colSpan={5} className="report__indent" style={depthStyle(node.depth)}>
          {node.label}
        </Td>
      </tr>

      {node.children.map((child) => (
        <GroupRows key={`${path}/${child.key}`} node={child} trail={path} names={names} />
      ))}

      {node.transactions.map((entry) => (
        <tr key={entry.split_id ?? entry.transaction_id}>
          <Td style={depthStyle(node.depth + 1)} className="report__leaf report__indent">
            <Clipped text={entry.payee} />
          </Td>
          <Td className="nowrap">{formatDate(entry.on, 'full')}</Td>
          <Td>
            <Clipped text={names.account(entry.account_id)} />
          </Td>
          <Td>
            <Clipped text={names.category(entry.category_id)} />
          </Td>
          <Td numeric>
            <Money value={entry.amount} tone="neutral" />
          </Td>
        </tr>
      ))}

      <tr className="report__subtotal">
        <Td colSpan={4} className="report__indent" style={depthStyle(node.depth)}>
          Total {node.label}
        </Td>
        <Td numeric>
          <Money value={node.total} tone="neutral" />
        </Td>
      </tr>
    </>
  )
}

function PivotRow({
  row,
  columns,
}: {
  row: import('@/lib/clients/reports').ReportPivotRow
  columns: import('@/lib/clients/reports').ReportColumn[]
}) {
  return (
    <tr className="report__leaf">
      <Td className="pivot__head">
        <Clipped text={row.label} />
      </Td>
      {row.cells.map((cell, index) => (
        <Td key={columns[index].key} numeric>
          <Money value={cell} tone="neutral" />
        </Td>
      ))}
      <Td numeric>
        <Money value={row.total} tone="neutral" />
      </Td>
    </tr>
  )
}

export function SummaryReport({
  result,
  heading,
  grain,
}: {
  result: ReportSummaryResult
  heading: string
  grain: TimeGrain
}) {
  if (result.rows.length === 0) {
    return (
      <Table density="sm">
        <tbody>
          <TableEmptyRow colSpan={2}>No transactions fall inside this report.</TableEmptyRow>
        </tbody>
      </Table>
    )
  }

  return (
    <Table density="sm" className="pivot">
      <thead>
        <tr>
          <Th className="pivot__head">{heading}</Th>
          {result.columns.map((column) => (
            <Th key={column.key} numeric>
              {columnHeading(column.key, column.label, result.column_dimension, grain)}
            </Th>
          ))}
          <Th numeric>Total</Th>
        </tr>
      </thead>
      <tbody>
        {result.sections.length === 0 ? (
          result.rows.map((row) => <PivotRow key={row.key} row={row} columns={result.columns} />)
        ) : (
          // Two families or more: each gets its header, rows and the engine's
          // own subtotal, Income above Expenses.
          result.sections.map((section) => (
            <Fragment key={section.key}>
              <tr className="report__group">
                <Td className="pivot__head" colSpan={result.columns.length + 2}>
                  {section.label}
                </Td>
              </tr>
              {result.rows
                .filter((row) => row.section === section.key)
                .map((row) => (
                  <PivotRow key={row.key} row={row} columns={result.columns} />
                ))}
              <tr className="report__subtotal">
                <Td className="pivot__head">Total {section.label}</Td>
                {section.cells.map((cell, index) => (
                  <Td key={result.columns[index].key} numeric>
                    <Money value={cell} tone="neutral" />
                  </Td>
                ))}
                <Td numeric>
                  <Money value={section.total} tone="neutral" />
                </Td>
              </tr>
            </Fragment>
          ))
        )}
        <tr className="report__grand">
          <Td className="pivot__head">Total</Td>
          {result.column_totals.map((total, index) => (
            <Td key={result.columns[index].key} numeric>
              <Money value={total} tone="neutral" />
            </Td>
          ))}
          <Td numeric>
            <Money value={result.total} tone="neutral" />
          </Td>
        </tr>
      </tbody>
    </Table>
  )
}

/** One line, cut with an ellipsis; the whole text is the tooltip. */
function Clipped({ text }: { text: string }) {
  return (
    <span className="cell__clip" title={text}>
      {text}
    </span>
  )
}
