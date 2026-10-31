import { useState } from 'react'

import { seriesColor } from '@/components/charts'
import { SortableTh, Table, TableEmptyRow, Td, Th } from '@/components/ui'
import type { SpendingGrain, SpendingGroup, SpendingReportData, SpendingRow } from '@/lib/clients/reports'
import {
  barPercent,
  barScale,
  barStyle,
  comparisonCaption,
  groupHeading,
  sortByDifference,
  sortRows,
} from '@/lib/reports/spending'

import { DifferenceChip, SpendAmount, type DifferenceUnit } from './amounts'

type Column = 'name' | 'spend' | 'difference'

/**
 * Each line's spend this period as a solid bar over the comparison's as a
 * hatched one, on one scale, with the change beside them. A line keeps its
 * colour through every sort, except that a side netting to a credit is drawn
 * by its size in the income colour.
 */
export function CompareBars({
  data,
  rows,
  grain,
  group,
  unit,
  showCents,
  colorOf,
  onOpen,
}: {
  data: SpendingReportData
  /** The lines to show, already searched. */
  rows: readonly SpendingRow[]
  grain: SpendingGrain
  group: SpendingGroup
  unit: DifferenceUnit
  showCents: boolean
  colorOf: (key: string) => number
  onOpen: (row: SpendingRow) => void
}) {
  const [sort, setSort] = useState<{ column: Column; direction: 'asc' | 'desc' }>({
    column: 'spend',
    direction: 'desc',
  })
  const comparing = data.comparison !== null
  const scale = barScale(data.rows)

  const ordered =
    sort.column === 'difference'
      ? sortByDifference(rows, sort.direction)
      : sort.column === 'name'
        ? sortRows(rows, sort.direction === 'asc' ? 'az' : 'za')
        : sortRows(rows, sort.direction === 'desc' ? 'largest' : 'smallest')
  const sortOn = (column: Column) =>
    setSort((current) => ({
      column,
      direction: current.column === column && current.direction === 'desc' ? 'asc' : 'desc',
    }))
  const direction = (column: Column) => (sort.column === column ? sort.direction : null)

  return (
    <Table lines={2} className="spend-bars">
      <thead>
        <tr>
          <SortableTh label={groupHeading(group)} direction={direction('name')} onSort={() => sortOn('name')} />
          <SortableTh
            label={data.comparison ? `Spend vs. ${comparisonCaption(data.comparison, grain)}` : 'Spend'}
            direction={direction('spend')}
            onSort={() => sortOn('spend')}
          />
          {comparing ? (
            <SortableTh
              label="Difference"
              numeric
              direction={direction('difference')}
              onSort={() => sortOn('difference')}
            />
          ) : (
            <Th />
          )}
        </tr>
      </thead>
      <tbody>
        {ordered.length === 0 ? (
          <TableEmptyRow colSpan={3}>Nothing spent in this period.</TableEmptyRow>
        ) : (
          ordered.map((row) => (
            <tr key={row.key}>
              <Td className="spend-bars__name">
                <button type="button" className="spend-link spend-link--clip" title={row.label} onClick={() => onOpen(row)}>
                  {row.label}
                </button>
              </Td>
              <Td
                className="spend-bars__cell"
                style={barStyle(seriesColor(colorOf(row.key)))}
              >
                <span className="spend-bars__line">
                  <span
                    className={row.amount > 0 ? 'spend-bars__bar spend-bars__bar--credit' : 'spend-bars__bar'}
                    style={{ width: `${barPercent(row.amount, scale)}%` }}
                  />
                  <span className="spend-bars__figure">
                    <SpendAmount value={row.amount} showCents={showCents} />
                  </span>
                </span>
                {comparing ? (
                  <span className="spend-bars__line spend-bars__line--was">
                    <span
                      className={
                        row.comparison > 0
                          ? 'spend-bars__bar spend-bars__bar--was spend-bars__bar--credit'
                          : 'spend-bars__bar spend-bars__bar--was'
                      }
                      style={{ width: `${barPercent(row.comparison, scale)}%` }}
                    />
                    <span className="spend-bars__figure">
                      <SpendAmount value={row.comparison} showCents={showCents} />
                    </span>
                  </span>
                ) : null}
              </Td>
              <Td numeric>
                <DifferenceChip
                  difference={row.difference}
                  unit={unit}
                  partial={data.period.partial}
                  showCents={showCents}
                />
              </Td>
            </tr>
          ))
        )}
      </tbody>
    </Table>
  )
}
