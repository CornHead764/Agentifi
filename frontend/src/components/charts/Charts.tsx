/**
 * Chart primitives, over recharts. Three rules hold across every chart:
 *
 * 1. **Colour comes from tokens.** `var(--series-n)` and the two money
 *    colours, nothing else.
 * 2. **Privacy mode reaches the chart.** Ticks go through `useMoneyTick` and
 *    tooltips render `<Money>`.
 * 3. **Tooltips read our own data, not recharts' payload**, so `Money` stays a
 *    `Money` to the glyph.
 */

import type { ReactNode } from 'react'
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

import { Money } from '@/components/Money'
import { formatPercentUnits } from '@/lib/format'
import { moneyFromCents, moneyToNumber, type Money as MoneyValue } from '@/lib/money'
import { scaled } from '@/lib/scale'

import { hoveredRow } from './hovered'
import {
  ACCENT_COLOR,
  AXIS,
  BAR_CURSOR,
  GRID,
  INCOME_COLOR,
  LINE_CURSOR,
  useMoneyTick,
  usePercentTick,
} from './palette'

// The per-period net overlay. Neutral: coral only ever means money out, and
// net swings both ways.
const NET_LINE_COLOR = 'var(--text-muted)'

export interface TooltipRow {
  label: string
  value: MoneyValue
  color?: string
  signs?: 'stored' | 'spend' | 'absolute'
}

/** The one tooltip body. Amounts go through `<Money>`; nothing else prints one. */
export function ChartTooltip({
  title,
  rows,
  hint,
}: {
  title: ReactNode
  rows: readonly TooltipRow[]
  /** A muted last line saying what clicking does. */
  hint?: ReactNode
}) {
  return (
    <div className="chart-tip">
      <p className="chart-tip__title">{title}</p>
      {rows.map((row) => (
        <p className="row chart-tip__row" key={row.label}>
          {row.color ? <span className="chart-tip__swatch" style={{ background: row.color }} /> : null}
          <span className="chart-tip__label">{row.label}</span>
          <Money value={row.value} signs={row.signs ?? 'stored'} tone="neutral" />
        </p>
      ))}
      {hint ? <p className="chart-tip__hint">{hint}</p> : null}
    </div>
  )
}

/* ---- Area trend: net worth, investment balances, the dashboard sparkline --- */

export interface TrendPoint {
  key: string
  label: string
  value: MoneyValue
}

export interface AreaTrendProps {
  points: readonly TrendPoint[]
  color?: string
  height?: number
  /** Off by default: an axis forced to zero flattens every real move. */
  startAtZero?: boolean
  /** Hidden on the dashboard tiles, where the card is 120px tall. */
  showAxes?: boolean
}

export function AreaTrend({
  points,
  color = ACCENT_COLOR,
  height = 260,
  startAtZero = false,
  showAxes = true,
}: AreaTrendProps) {
  const tick = useMoneyTick()
  const data = points.map((point) => ({ ...point, plot: moneyToNumber(point.value) }))
  const gradientId = `trend-${color.replace(/[^a-z0-9]/gi, '')}`

  return (
    <ResponsiveContainer width="100%" height={scaled(height)}>
      <AreaChart data={data} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
        <defs>
          <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={color} stopOpacity={0.55} />
            <stop offset="100%" stopColor={color} stopOpacity={0.05} />
          </linearGradient>
        </defs>
        {showAxes ? <CartesianGrid {...GRID} vertical={false} /> : null}
        <XAxis dataKey="label" hide={!showAxes} {...AXIS} minTickGap={40} />
        <YAxis
          tickFormatter={tick}
          hide={!showAxes}
          width={scaled(64)}
          domain={startAtZero ? [0, 'auto'] : ['auto', 'auto']}
          {...AXIS}
        />
        <Tooltip
          cursor={LINE_CURSOR}
          content={({ payload }) => {
            const point = hoveredRow<(typeof data)[number]>(payload)
            if (!point) return null
            return <ChartTooltip title={point.label} rows={[{ label: 'Value', value: point.value, color }]} />
          }}
        />
        <Area
          type="monotone"
          dataKey="plot"
          stroke={color}
          strokeWidth={1.5}
          fill={`url(#${gradientId})`}
          isAnimationActive={false}
        />
      </AreaChart>
    </ResponsiveContainer>
  )
}

/* ---- Bars: the two six-month dashboard charts, and report summaries -------- */

export interface BarPoint {
  key: string
  label: string
  value: MoneyValue
}

/**
 * The bar's height under the same sign convention its label is printed in,
 * so spending bars do not hang down beside income bars.
 */
function plotValue(value: MoneyValue, signs: 'stored' | 'spend' | 'absolute'): number {
  const plain = moneyToNumber(value)
  switch (signs) {
    case 'absolute':
      return Math.abs(plain)
    case 'spend':
      return -plain
    default:
      return plain
  }
}

export function BarSeries({
  points,
  color = ACCENT_COLOR,
  height = 180,
  signs = 'stored',
}: {
  points: readonly BarPoint[]
  color?: string
  height?: number
  signs?: 'stored' | 'spend' | 'absolute'
}) {
  const tick = useMoneyTick()
  const data = points.map((point) => ({ ...point, plot: plotValue(point.value, signs) }))

  return (
    <ResponsiveContainer width="100%" height={scaled(height)}>
      <BarChart data={data} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
        <CartesianGrid {...GRID} vertical={false} />
        <XAxis dataKey="label" {...AXIS} />
        <YAxis tickFormatter={tick} width={scaled(56)} {...AXIS} />
        <ReferenceLine y={0} stroke="var(--border-strong)" />
        <Tooltip
          cursor={BAR_CURSOR}
          content={({ payload }) => {
            const point = hoveredRow<(typeof data)[number]>(payload)
            if (!point) return null
            return <ChartTooltip title={point.label} rows={[{ label: 'Total', value: point.value, color, signs }]} />
          }}
        />
        <Bar dataKey="plot" fill={color} radius={[3, 3, 0, 0]} isAnimationActive={false} />
      </BarChart>
    </ResponsiveContainer>
  )
}

/* ---- Stacked bars: the summary report's chart ------------------------------ */

export interface StackedSeries {
  key: string
  label: string
  color: string
}

export interface StackedPoint {
  key: string
  label: string
  /** One entry per series key. Missing means zero for that series. */
  values: Readonly<Record<string, MoneyValue>>
}

export function StackedBars({
  series,
  points,
  height = 260,
  netLine,
}: {
  series: readonly StackedSeries[]
  points: readonly StackedPoint[]
  height?: number
  /** Income & Expense draws the per-period net as a dashed line over the bars. */
  netLine?: Readonly<Record<string, MoneyValue>>
}) {
  const tick = useMoneyTick()
  const data = points.map((point) => {
    const plotted: Record<string, number> = {}
    for (const one of series) plotted[one.key] = moneyToNumber(point.values[one.key] ?? 0)
    if (netLine) plotted.__net = moneyToNumber(netLine[point.key] ?? 0)
    return { label: point.label, key: point.key, ...plotted }
  })

  return (
    <ResponsiveContainer width="100%" height={scaled(height)}>
      <BarChart data={data} stackOffset="sign" margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
        <CartesianGrid {...GRID} vertical={false} />
        <XAxis dataKey="label" {...AXIS} minTickGap={16} />
        <YAxis tickFormatter={tick} width={scaled(64)} {...AXIS} />
        <ReferenceLine y={0} stroke="var(--border-strong)" />
        <Tooltip
          cursor={BAR_CURSOR}
          content={({ payload }) => {
            const hovered = hoveredRow<(typeof data)[number]>(payload)
            const point = hovered && points.find((entry) => entry.key === hovered.key)
            if (!point) return null
            const rows = series
              .map((one) => ({ label: one.label, value: point.values[one.key] ?? 0, color: one.color }))
              .filter((row) => row.value !== 0)
            if (netLine) rows.push({ label: 'Net', value: netLine[point.key] ?? 0, color: NET_LINE_COLOR })
            return <ChartTooltip title={point.label} rows={rows} />
          }}
        />
        {series.map((one) => (
          <Bar
            key={one.key}
            dataKey={one.key}
            stackId="stack"
            fill={one.color}
            isAnimationActive={false}
          />
        ))}
        {netLine ? (
          <Line
            type="monotone"
            dataKey="__net"
            stroke={NET_LINE_COLOR}
            strokeDasharray="4 3"
            dot={{ r: 2 }}
            isAnimationActive={false}
          />
        ) : null}
      </BarChart>
    </ResponsiveContainer>
  )
}

/* ---- Multi-line: cash flow per account, performance per account ------------ */

export interface LineMarker {
  /** What sits at this point: "Mortgage", "Payroll". */
  label: string
  /** The occurrence's own amount, shown in the tooltip beside the label. */
  amount: number | null
  /** Income draws the larger badge with the glyph, Simplifi's cue. */
  emphasis?: boolean
}

export interface LineSeries {
  key: string
  label: string
  color: string
  /** `null` where the figure is unknown, which draws a gap rather than a zero. */
  points: Readonly<Record<string, number | null>>
  /** Occurrence markers keyed by axis point — a dot on the line, rows in the tooltip. */
  markers?: Readonly<Record<string, readonly LineMarker[]>>
  /** A projection draws dashed, the way Simplifi separates it from history. */
  dashed?: boolean
}

/**
 * The dot recharts calls for every point of a marked line. Nothing renders on
 * unmarked points; a marked one gets a filled dot in the line's own colour,
 * and an emphasised one (income, in the cash flow) the larger badge.
 */
function markerDot(series: LineSeries) {
  return function MarkerDot(props: { cx?: number; cy?: number; payload?: { key?: string } }) {
    const key = props.payload?.key
    const at = key === undefined ? undefined : series.markers?.[key]
    if (at === undefined || at.length === 0 || props.cx === undefined || props.cy === undefined) {
      return <g key={`${series.key}-${key ?? 'none'}`} />
    }
    const emphasized = at.some((marker) => marker.emphasis === true)
    if (emphasized) {
      return (
        <g key={`${series.key}-${key}`}>
          <circle cx={props.cx} cy={props.cy} r={7} fill={INCOME_COLOR} />
          <text
            x={props.cx}
            y={props.cy + 3}
            textAnchor="middle"
            fontSize={9}
            fontWeight={700}
            fill="var(--surface-0)"
          >
            $
          </text>
        </g>
      )
    }
    return (
      <circle
        key={`${series.key}-${key}`}
        cx={props.cx}
        cy={props.cy}
        r={3.5}
        fill={series.color}
        stroke="var(--surface-0)"
        strokeWidth={1}
      />
    )
  }
}

export function MultiLine({
  axis,
  series,
  height = 280,
  unit = 'money',
  startAtZero = false,
  markAt,
}: {
  /** The shared x axis, in order. */
  axis: readonly { key: string; label: string }[]
  series: readonly LineSeries[]
  height?: number
  unit?: 'money' | 'percent'
  /** Off by default: an axis forced to zero flattens every real move. */
  startAtZero?: boolean
  /** A vertical rule at one axis key — "Today", where history meets projection. */
  markAt?: { key: string; label: string }
}) {
  const moneyTick = useMoneyTick()
  const percentTick = usePercentTick()
  // `key` and `label` are named on the row type rather than left to the index
  // signature, so the tooltip can read the hovered row's key back out and
  // index a series with it.
  const data: ({ key: string; label: string } & Record<string, number | null | string>)[] =
    axis.map((point) => {
      const row: Record<string, number | null | string> = {}
      for (const one of series) row[one.key] = one.points[point.key] ?? null
      return { ...row, key: point.key, label: point.label }
    })

  return (
    <>
      <ResponsiveContainer width="100%" height={scaled(height)}>
        <LineChart data={data} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
          <CartesianGrid {...GRID} vertical={false} />
          <XAxis dataKey="label" {...AXIS} minTickGap={32} />
          <YAxis
            tickFormatter={unit === 'money' ? moneyTick : percentTick}
            width={scaled(64)}
            domain={startAtZero ? [0, 'auto'] : ['auto', 'auto']}
            {...AXIS}
          />
          {unit === 'percent' ? <ReferenceLine y={0} stroke="var(--border-strong)" /> : null}
          {markAt ? (
            <ReferenceLine
              x={axis.find((point) => point.key === markAt.key)?.label}
              stroke="var(--border-strong)"
              strokeDasharray="2 2"
              label={{
                value: markAt.label,
                position: 'insideTopLeft',
                fill: 'var(--text-muted)',
                fontSize: scaled(11),
              }}
            />
          ) : null}
          <Tooltip
            cursor={LINE_CURSOR}
            content={({ payload }) => {
              const point = hoveredRow<(typeof data)[number]>(payload)
              if (!point) return null
              return (
                <div className="chart-tip">
                  <p className="chart-tip__title">{point.label}</p>
                  {series.map((one) => {
                    const value = one.points[point.key]
                    if (value === null || value === undefined) return null
                    return (
                      <p className="row chart-tip__row" key={one.key}>
                        <span className="chart-tip__swatch" style={{ background: one.color }} />
                        <span className="chart-tip__label">{one.label}</span>
                        {unit === 'money' ? (
                          <PrivateNumber value={value} />
                        ) : (
                          <span className="money">{formatPercentUnits(value, { digits: 2, showPlus: true })}</span>
                        )}
                      </p>
                    )
                  })}
                  {series.flatMap((one) =>
                    (one.markers?.[point.key] ?? []).map((marker, index) => (
                      <p
                        className="row chart-tip__row chart-tip__row--marker"
                        key={`${one.key}-marker-${index}`}
                      >
                        <span className="chart-tip__swatch" style={{ background: one.color }} />
                        <span className="chart-tip__label">{marker.label}</span>
                        {marker.amount === null ? null : <PrivateNumber value={marker.amount} />}
                      </p>
                    )),
                  )}
                </div>
              )
            }}
          />
          {series.map((one) => (
            <Line
              key={one.key}
              dataKey={one.key}
              name={one.label}
              type="monotone"
              stroke={one.color}
              strokeWidth={1.5}
              strokeDasharray={one.dashed === true ? '4 3' : undefined}
              dot={one.markers === undefined ? false : markerDot(one)}
              connectNulls={false}
              isAnimationActive={false}
            />
          ))}
        </LineChart>
      </ResponsiveContainer>
      <ChartLegend entries={series} />
    </>
  )
}

/**
 * Which side of its reference line a marker's label hangs on. Centred, except
 * near an edge, where the SVG would cut a centred label off.
 */
function markerAnchor(axis: readonly { key: string }[], at: string): 'start' | 'middle' | 'end' {
  const index = axis.findIndex((point) => point.key === at)
  if (index < 0 || axis.length < 2) return 'middle'
  const along = index / (axis.length - 1)
  if (along > 0.75) return 'end'
  if (along < 0.25) return 'start'
  return 'middle'
}

/**
 * A projection with an estimate band (the retirement chart): high, expected
 * and low drawn back to front as filled areas from zero, with reference lines
 * at the years that matter.
 */
export function BandedProjection({
  axis,
  high,
  expected,
  low,
  markers = [],
  height = 300,
}: {
  axis: readonly { key: string; label: string }[]
  high: Readonly<Record<string, number | null>>
  expected: Readonly<Record<string, number | null>>
  low: Readonly<Record<string, number | null>>
  markers?: readonly { at: string; label: string }[]
  height?: number
}) {
  const moneyTick = useMoneyTick()
  const data: ({ key: string; label: string } & Record<string, number | null | string>)[] =
    axis.map((point) => ({
      key: point.key,
      label: point.label,
      high: high[point.key] ?? null,
      expected: expected[point.key] ?? null,
      low: low[point.key] ?? null,
    }))

  const bands = [
    { key: 'high', label: 'High estimate', color: 'var(--series-2)', opacity: 0.25, dashed: true },
    { key: 'expected', label: 'Expected', color: 'var(--accent)', opacity: 0.45, dashed: false },
    { key: 'low', label: 'Low estimate', color: 'var(--text-faint)', opacity: 0.35, dashed: true },
  ] as const

    // Close marker labels overlap at phone widths, so they alternate rows; the
    // top margin makes room for the higher one.
  const markerRow = scaled(13)
  const rows = Math.min(markers.length, 2)

  return (
    <>
      <ResponsiveContainer width="100%" height={scaled(height)}>
        <AreaChart
          data={data}
          margin={{ top: 20 + (rows - 1) * markerRow, right: 8, bottom: 0, left: 0 }}
        >
          <CartesianGrid {...GRID} vertical={false} />
          <XAxis dataKey="label" {...AXIS} minTickGap={24} />
          <YAxis
            tickFormatter={moneyTick}
            width={scaled(64)}
            {...AXIS}
          />
          <Tooltip
            cursor={LINE_CURSOR}
            content={({ payload }) => {
              const point = hoveredRow<(typeof data)[number]>(payload)
              if (!point) return null
              return (
                <div className="chart-tip">
                  <p className="chart-tip__title">{point.label}</p>
                  {bands.map((band) => {
                    const value = point[band.key]
                    if (typeof value !== 'number') return null
                    return (
                      <p className="row chart-tip__row" key={band.key}>
                        <span className="chart-tip__swatch" style={{ background: band.color }} />
                        <span className="chart-tip__label">{band.label}</span>
                        <PrivateNumber value={value} />
                      </p>
                    )
                  })}
                </div>
              )
            }}
          />
          {bands.map((band) => (
            <Area
              key={band.key}
              dataKey={band.key}
              name={band.label}
              type="monotone"
              stroke={band.color}
              strokeWidth={1.25}
              strokeDasharray={band.dashed ? '4 3' : undefined}
              fill={band.color}
              fillOpacity={band.opacity}
              connectNulls={false}
              isAnimationActive={false}
            />
          ))}
          {markers.map((marker, index) => {
            const anchor = markerAnchor(axis, marker.at)
            return (
              <ReferenceLine
                key={marker.at}
                x={axis.find((point) => point.key === marker.at)?.label}
                stroke="var(--income)"
                strokeWidth={1.25}
                label={{
                  value: marker.label,
                  position: 'top',
                  textAnchor: anchor,
                  dx: anchor === 'end' ? -4 : anchor === 'start' ? 4 : 0,
                  dy: (index % 2 === 0 ? -(rows - 1) : 0) * markerRow,
                  fill: 'var(--text-muted)',
                  fontSize: scaled(11),
                }}
              />
            )
          })}
        </AreaChart>
      </ResponsiveContainer>
      <ChartLegend entries={bands} />
    </>
  )
}

/**
 * Per-series areas stacked into one total — the balances chart's per-account
 * view. The same series shape MultiLine takes, so a page can offer both off
 * one data pass; the stack order is the series order given.
 */
export function StackedAreas({
  axis,
  series,
  height = 280,
}: {
  axis: readonly { key: string; label: string }[]
  series: readonly LineSeries[]
  height?: number
}) {
  const moneyTick = useMoneyTick()
  const data: ({ key: string; label: string } & Record<string, number | null | string>)[] =
    axis.map((point) => {
      const row: Record<string, number | null | string> = {}
      // A gap plots as zero here rather than as null: one account's missing
      // day would otherwise tear a hole through every layer stacked above it.
      for (const one of series) row[one.key] = one.points[point.key] ?? 0
      return { ...row, key: point.key, label: point.label }
    })

  return (
    <>
      <ResponsiveContainer width="100%" height={scaled(height)}>
        <AreaChart data={data} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
          <CartesianGrid {...GRID} vertical={false} />
          <XAxis dataKey="label" {...AXIS} minTickGap={32} />
          <YAxis
            tickFormatter={moneyTick}
            width={scaled(64)}
            {...AXIS}
          />
          <Tooltip
            cursor={LINE_CURSOR}
            content={({ payload }) => {
              const point = hoveredRow<(typeof data)[number]>(payload)
              if (!point) return null
              return (
                <div className="chart-tip">
                  <p className="chart-tip__title">{point.label}</p>
                  {series.map((one) => {
                    const value = one.points[point.key]
                    if (value === null || value === undefined) return null
                    return (
                      <p className="row chart-tip__row" key={one.key}>
                        <span className="chart-tip__swatch" style={{ background: one.color }} />
                        <span className="chart-tip__label">{one.label}</span>
                        <PrivateNumber value={value} />
                      </p>
                    )
                  })}
                </div>
              )
            }}
          />
          {series.map((one) => (
            <Area
              key={one.key}
              dataKey={one.key}
              name={one.label}
              stackId="stack"
              type="monotone"
              stroke={one.color}
              fill={one.color}
              fillOpacity={0.35}
              strokeWidth={1}
              isAnimationActive={false}
            />
          ))}
        </AreaChart>
      </ResponsiveContainer>
      <ChartLegend entries={series} />
    </>
  )
}

/**
 * The legend under a multi-series chart, drawn in the page: recharts lays its
 * legend inside the chart's box at a fixed height, so a wrapping legend paints
 * over whatever sits below.
 */
function ChartLegend({
  entries,
}: {
  entries: readonly { key: string; label: string; color: string; dashed?: boolean }[]
}) {
  return (
    <ul className="chart-legend">
      {entries.map((one) => (
        <li key={one.key} className="chart-legend__item">
          <span
            className={
              one.dashed === true
                ? 'chart-legend__swatch chart-legend__swatch--dashed'
                : 'chart-legend__swatch'
            }
            style={one.dashed === true ? { color: one.color } : { background: one.color }}
          />
          <span className="hint">{one.label}</span>
        </li>
      ))}
    </ul>
  )
}

/**
 * A chart figure already in major units. Charts plot floats, so this rounds
 * back to cents for `<Money>`, which keeps privacy masking.
 */
function PrivateNumber({ value }: { value: number }) {
  return <Money value={moneyFromCents(Math.round(value * 100))} tone="neutral" />
}

/* ---- Donut: top spending categories, the breakdown -------------------------
   In `./Donut`, with the legend and the hover state the two share. */
