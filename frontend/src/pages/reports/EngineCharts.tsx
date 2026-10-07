import { useState } from 'react'

import {
  Donut,
  DonutGroup,
  DonutLegend,
  DrillTrail,
  StackedBars,
  type DonutSlice,
} from '@/components/charts'
import { seriesColor } from '@/components/charts/palette'
import {
  type ReportConfig,
  type ReportNode,
  type ReportResult,
  type ReportSummaryResult,
} from '@/lib/clients/reports'
import { moneyToNumber, sumMoney } from '@/lib/money'
import { drillTo, trailKeys } from '@/lib/reports/donut'
import { columnHeading, DIMENSION_LABELS } from '@/lib/reports/labels'

/** The pivot as stacked bars: one stack per column, one band per row. */
export function PivotChart({
  summary,
  grain,
}: {
  summary: ReportSummaryResult
  grain: ReportConfig['time_grain']
}) {
  const largest = [...summary.rows]
    .sort((a, b) => Math.abs(moneyToNumber(b.total)) - Math.abs(moneyToNumber(a.total)))
    .slice(0, 8)
  const series = largest.map((row, index) => ({
    key: row.key,
    label: row.label,
    color: seriesColor(index),
  }))

  const points = summary.columns.map((column, columnIndex) => ({
    key: column.key,
    label: columnHeading(column.key, column.label, summary.column_dimension, grain),
    values: Object.fromEntries(
      largest.map((row) => [row.key, row.cells[columnIndex]]),
    ),
  }))

  // The net line is the engine's own column totals — income plus expenses per
  // period — never a second query and never a sum taken here.
  const netLine = Object.fromEntries(
    summary.columns.map((column, index) => [column.key, summary.column_totals[index]]),
  )

  return <StackedBars series={series} points={points} netLine={netLine} />
}

/**
 * The donut, and the drill-down it is the control for. Clicking a parent
 * redraws the ring as its subcategories with no fetch: the report is already
 * the whole tree. The path is held here rather than on the tab, so drilling
 * does not make a report "unsaved", and is re-resolved against every result.
 */
export function DonutRow({ result }: { result: ReportResult }) {
  const [path, setPath] = useState<readonly string[]>([])
  const level = drillTo(result.transaction?.groups ?? [], path)
  const dimension = DIMENSION_LABELS[result.config.rows].toLowerCase()
  const slices = donutSlices(level.nodes)

  const open = (slice: DonutSlice) => setPath([...trailKeys(level), slice.key])

  return (
    <>
      <DrillTrail
        label="Chart breakdown"
        root={`All ${dimension}`}
        trail={level.trail}
        onStep={(depth) => setPath(trailKeys(level).slice(0, depth))}
      />

      <DonutGroup className="donut-row" onSelect={open}>
        <Donut slices={slices} signs="stored" />
        <DonutLegend slices={slices} />
      </DonutGroup>
    </>
  )
}

/**
 * The tail collapses into "Everything else", which can be negative when
 * refunds outweigh spend; it is left signed. Only a group with something under
 * it is a click target, so neither the fold nor a leaf carries an `action`.
 */
function donutSlices(groups: readonly ReportNode[]): DonutSlice[] {
  const ranked = [...groups].sort((a, b) => Math.abs(b.total) - Math.abs(a.total))
  const head = ranked.slice(0, 10)
  const tail = ranked.slice(10)

  const slices: DonutSlice[] = head.map((group, index) => ({
    key: group.key,
    label: group.label,
    value: group.total,
    color: seriesColor(index),
    action: group.children.length > 0 ? `Break ${group.label} down` : undefined,
  }))

  if (tail.length > 0) {
    slices.push({
      key: '__else',
      label: 'Everything else',
      value: sumMoney(tail.map((group) => group.total)),
      color: 'var(--text-faint)',
    })
  }
  return slices
}
