import { useCallback, useMemo } from 'react'

import {
  AggLegendRow,
  Donut,
  DonutGroup,
  seriesColor,
  type DonutSlice,
} from '@/components/charts'
import { EmptyState } from '@/components/ui'
import type { SpendingRow } from '@/lib/clients/reports'
import { formatPercent, parseRate } from '@/lib/format'

import { SpendAmount } from './amounts'

/**
 * The period's spending as shares of a ring, laid out as the register's
 * Spending tab lays its own. A line that nets to a credit has no wedge and no
 * share; it is in the legend with its "+".
 */
export function SpendingDonut({
  rows,
  showCents,
  colorOf,
  onOpen,
}: {
  /** In the legend's order. */
  rows: readonly SpendingRow[]
  showCents: boolean
  colorOf: (key: string) => number
  onOpen: (row: SpendingRow) => void
}) {
  const slices = useMemo<DonutSlice[]>(
    () =>
      rows
        .filter((row) => row.amount < 0)
        .map((row) => ({
          key: row.key,
          label: row.label,
          value: row.amount,
          color: seriesColor(colorOf(row.key)),
          action: `Open ${row.label}`,
        })),
    [rows, colorOf],
  )
  const select = useCallback(
    (slice: DonutSlice) => {
      const row = rows.find((one) => one.key === slice.key)
      if (row) onOpen(row)
    },
    [rows, onOpen],
  )

  if (rows.length === 0) return <EmptyState compact title="Nothing spent in this period." />
  // A line with nothing this period is in the comparison only; the ring has no place for it.
  const lines = rows.filter((row) => row.amount !== 0)

  return (
    <DonutGroup className="aggregate__pair" onSelect={select}>
      <div className="aggregate__donut">
        <Donut height={220} signs="spend" slices={slices} />
      </div>
      <div className="agg-legend">
        {lines.map((row) => {
          const slice = slices.find((one) => one.key === row.key)
          return slice ? (
            <SliceEntry key={row.key} slice={slice} row={row} showCents={showCents} />
          ) : (
            <button
              key={row.key}
              type="button"
              className="agg-legend__row agg-legend__row--target spend-link"
              onClick={() => onOpen(row)}
            >
              <span className="agg-legend__name">
                <span className="agg-legend__swatch" style={{ background: seriesColor(colorOf(row.key)) }} />
                {row.label}
              </span>
              <span className="agg-legend__value">
                <SpendAmount value={row.amount} showCents={showCents} />
              </span>
            </button>
          )
        })}
      </div>
    </DonutGroup>
  )
}

/** A legend entry tied to its wedge: hovering either lights both, clicking opens the line. */
function SliceEntry({ slice, row, showCents }: { slice: DonutSlice; row: SpendingRow; showCents: boolean }) {
  return (
    <AggLegendRow
      slice={slice}
      label={row.label}
      figure={
        <>
          <SpendAmount value={row.amount} showCents={showCents} />{' '}
          <span className="muted">({formatPercent(parseRate(row.share), { digits: 1 })})</span>
        </>
      }
    />
  )
}
