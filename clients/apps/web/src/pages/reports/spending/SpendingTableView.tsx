import { useState } from 'react'

import { SortButton, SortableTh, Table, TableEmptyRow, Td, Th } from '@/components/ui'
import type {
  SpendingGrain,
  SpendingGroup,
  SpendingReportData,
  SpendingTableRow,
} from '@/lib/clients/reports'
import { EM_DASH } from '@/lib/format'
import {
  groupHeading,
  heatLevel,
  heatScale,
  periodTick,
  searchRows,
  sortTable,
  sparkPoints,
  type TableColumn,
} from '@/lib/reports/spending'

import { DifferenceChip, SpendAmount, type DifferenceUnit } from './amounts'

const SPARK_WIDTH = 56
const SPARK_HEIGHT = 16

/**
 * Every line across every period, each cell shaded by how much of the most
 * any one cell holds; the last period against the one before it, cut to the
 * same days, and the line's course as a sparkline. Wide, so it scrolls in
 * its own box.
 */
export function SpendingTableView({
  data,
  grain,
  group,
  search,
  unit,
  showCents,
  onOpen,
}: {
  data: SpendingReportData
  grain: SpendingGrain
  group: SpendingGroup
  search: string
  unit: DifferenceUnit
  showCents: boolean
  onOpen: (row: SpendingTableRow) => void
}) {
  const { periods, prior } = data.table
  const [sort, setSort] = useState<{ column: TableColumn; direction: 'asc' | 'desc' }>({
    column: periods.length - 1,
    direction: 'desc',
  })
  const most = heatScale(data.table.rows)
  const rows = sortTable(searchRows(data.table.rows, search), sort.column, sort.direction)
  const last = periods[periods.length - 1]

  const sortOn = (column: TableColumn) =>
    setSort((current) => ({
      column,
      direction: current.column === column && current.direction === 'desc' ? 'asc' : 'desc',
    }))
  const direction = (column: TableColumn) => (sort.column === column ? sort.direction : null)

  return (
    <Table density="sm" className="spend-table">
      <thead>
        <tr>
          <Th
            className="pivot__head"
            aria-sort={
              sort.column === 'name' ? (sort.direction === 'asc' ? 'ascending' : 'descending') : undefined
            }
          >
            <SortButton label={groupHeading(group)} direction={direction('name')} onSort={() => sortOn('name')} />
          </Th>
          {periods.map((period, index) => (
            <SortableTh
              key={period.key}
              numeric
              label={periodTick(period, grain)}
              direction={direction(index)}
              onSort={() => sortOn(index)}
            />
          ))}
          <SortableTh numeric label="Total" direction={direction('total')} onSort={() => sortOn('total')} />
          <SortableTh
            numeric
            label={last ? `${periodTick(last, grain)} vs ${periodTick({ ...prior, partial: false }, grain)}` : 'Change'}
            direction={direction('difference')}
            onSort={() => sortOn('difference')}
          />
          <Th>Trend</Th>
        </tr>
      </thead>
      <tbody>
        {rows.length === 0 ? (
          <TableEmptyRow colSpan={periods.length + 4}>Nothing spent in these periods.</TableEmptyRow>
        ) : (
          rows.map((row) => (
            <tr key={row.key}>
              <Td className="pivot__head">
                <button type="button" className="spend-link spend-link--clip" title={row.label} onClick={() => onOpen(row)}>
                  {row.label}
                </button>
              </Td>
              {row.cells.map((cell, index) => (
                <Td
                  key={periods[index]?.key ?? index}
                  numeric
                  className={`spend-heat spend-heat--${heatLevel(cell, most)}`}
                >
                  {cell === 0 ? (
                    <span className="money money--absent">{EM_DASH}</span>
                  ) : (
                    <SpendAmount value={cell} showCents={showCents} />
                  )}
                </Td>
              ))}
              <Td numeric className="spend-table__total">
                <SpendAmount value={row.total} showCents={showCents} />
              </Td>
              <Td numeric>
                <DifferenceChip
                  difference={row.difference}
                  unit={unit}
                  partial={last?.partial ?? false}
                  showCents={showCents}
                />
              </Td>
              <Td>
                <svg
                  className="spend-spark"
                  width={SPARK_WIDTH}
                  height={SPARK_HEIGHT}
                  viewBox={`0 0 ${SPARK_WIDTH} ${SPARK_HEIGHT}`}
                  aria-hidden="true"
                >
                  <polyline points={sparkPoints(row.cells, SPARK_WIDTH, SPARK_HEIGHT)} />
                </svg>
              </Td>
            </tr>
          ))
        )}
      </tbody>
    </Table>
  )
}

/** The shading's key: four steps from less spend to more. */
export function HeatLegend() {
  return (
    <span className="toolbar__note spend-heat-legend" aria-hidden="true">
      Less
      {[1, 2, 3, 4].map((level) => (
        <span key={level} className={`spend-heat-legend__dot spend-heat--${level}`} />
      ))}
      More spend
    </span>
  )
}
