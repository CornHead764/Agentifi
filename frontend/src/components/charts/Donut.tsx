/**
 * The donut, and the legend beside it. A slice is a control: hovering either
 * half highlights both (`./donutFocus`), clicking goes to `DonutSlice.action`
 * when there is one, and the pop is CSS (`.donut__pop`, at `var(--motion)`),
 * offset by `./sliceGeometry`.
 *
 * The ring renders once and is then written to by hand: recharts regenerates
 * every sector node whenever `<Pie>` re-renders, which would hard-cut the
 * transition and drop keyboard focus. So the chart is memoized against its
 * data and the hover states are toggled as attributes by an effect.
 *
 * Each wedge is drawn twice: the lower copy is transparent, stays put and
 * owns the pointer, so the pop cannot move the wedge out from under it.
 *
 * The slice hangs off a field of its own: recharts spreads the data row into
 * the shape's props, where React would take `key` and recharts writes its own
 * numeric `value` over a `Money`.
 */

import { clsx } from 'clsx'
import { useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import {
  Cell,
  Pie,
  PieChart,
  ResponsiveContainer,
  Sector,
  Tooltip,
  type PieSectorShapeProps,
} from 'recharts'

import { Money } from '@/components/Money'
import { moneyToNumber, type SignConvention } from '@/lib/money'
import { scaled } from '@/lib/scale'

import { ChartTooltip } from './Charts'
import {
  DonutFocusContext,
  useDonutFocus,
  useSliceRowProps,
  type DonutFocus,
  type DonutSlice,
} from './donutFocus'
import { hoveredRow } from './hovered'
import { SLICE_POP, sliceOffset } from './sliceGeometry'

/**
 * A donut and its legend, wired to each other. Renders the element that lays
 * the two out, since the provider has to wrap both halves.
 */
export function DonutGroup({
  className,
  onSelect,
  children,
}: {
  className?: string
  /** Omitted where nothing is clickable; slices then ignore their `action`. */
  onSelect?: (slice: DonutSlice) => void
  children: ReactNode
}) {
  const [active, setActive] = useState<string | null>(null)
  // Both must be stable: the chart is memoized against them.
  const value = useMemo<DonutFocus>(
    () => ({ active, hover: setActive, select: onSelect }),
    [active, onSelect],
  )
  return (
    <DonutFocusContext value={value}>
      <div className={className}>{children}</div>
    </DonutFocusContext>
  )
}

interface PlotRow {
  slice: DonutSlice
  label: string
  plot: number
}

/** How far this wedge travels when it is the one being looked at. */
interface PopStyle extends CSSProperties {
  '--pop-x': string
  '--pop-y': string
}

function DonutSector({
  sector,
  slice,
  hover,
  select,
}: {
  sector: PieSectorShapeProps
  /** Absent for a sector this chart did not put there; that one is drawn plain. */
  slice: DonutSlice | undefined
  hover: (key: string | null) => void
  select?: (slice: DonutSlice) => void
}) {
  if (slice === undefined) return <Sector {...sector} />

  const target = slice.action !== undefined && select !== undefined
  const activate = () => {
    if (target) select?.(slice)
  }
  const { dx, dy } = sliceOffset(sector.midAngle ?? 0, SLICE_POP)
  const pop: PopStyle = { '--pop-x': `${dx}px`, '--pop-y': `${dy}px` }

  return (
    <g
      className={clsx('donut__slice', target && 'donut__slice--target')}
      data-slice={slice.key}
      // A slice is a tab stop only where it leads somewhere. A ring of eleven
      // wedges that go nowhere would otherwise be eleven stops to get past.
      tabIndex={target ? 0 : undefined}
      role={target ? 'button' : undefined}
      aria-label={target ? slice.action : undefined}
      onFocus={() => hover(slice.key)}
      onBlur={() => hover(null)}
      onKeyDown={(event) => {
        if (event.key !== 'Enter' && event.key !== ' ') return
        event.preventDefault()
        activate()
      }}
      // A touch has no hover to precede it, so the tap is the whole
      // interaction: it selects, and the legend does the highlighting.
      onClick={activate}
    >
      <Sector {...sector} className="donut__hit" fill="transparent" stroke="none" />
      <g className="donut__pop" style={pop}>
        <Sector {...sector} />
      </g>
    </g>
  )
}

export function Donut({
  slices,
  height = 200,
  signs = 'spend',
}: {
  slices: readonly DonutSlice[]
  height?: number
  signs?: 'stored' | 'spend' | 'absolute'
}) {
  const { active, hover, select } = useDonutFocus()
  const ring = useRef<HTMLDivElement>(null)

  const chart = useMemo(() => {
    const data: PlotRow[] = slices.map((slice) => ({
      slice,
      label: slice.label,
      plot: Math.abs(moneyToNumber(slice.value)),
    }))

    return (
      <ResponsiveContainer width="100%" height={scaled(height)}>
        <PieChart>
          <Tooltip
            content={({ payload }) => {
              const row = hoveredRow<PlotRow>(payload)
              if (!row) return null
              const slice = row.slice
              return (
                <ChartTooltip
                  title={slice.label}
                  rows={[{ label: 'Total', value: slice.value, color: slice.color, signs }]}
                />
              )
            }}
          />
          <Pie
            data={data}
            dataKey="plot"
            nameKey="label"
            innerRadius="58%"
            outerRadius="88%"
            paddingAngle={1}
            stroke="none"
            isAnimationActive={false}
            // Addressed by index: recharts types the merged sector props as
            // unknowable.
            shape={(sector: PieSectorShapeProps) => (
              <DonutSector
                sector={sector}
                slice={slices[sector.index]}
                hover={hover}
                select={select}
              />
            )}
            onMouseEnter={(_entry, index) => hover(slices[index]?.key ?? null)}
            onMouseLeave={() => hover(null)}
          >
            {data.map((row) => (
              <Cell key={row.slice.key} fill={row.slice.color} />
            ))}
          </Pie>
        </PieChart>
      </ResponsiveContainer>
    )
  }, [slices, height, signs, hover, select])

  // The hover, written onto the sectors rather than rendered into them (see
  // the file comment). `chart` is a dependency: a rebuilt ring is new nodes
  // with none of this on them.
  useEffect(() => {
    for (const node of ring.current?.querySelectorAll('.donut__slice') ?? []) {
      const on = node.getAttribute('data-slice') === active
      node.toggleAttribute('data-on', on)
      node.toggleAttribute('data-dim', active !== null && !on)
    }
  }, [active, chart])

  return (
    <div className="donut" ref={ring}>
      {chart}
    </div>
  )
}

/**
 * The donut's legend, wrapped into as many columns as fit. Swatch, label,
 * figure, in that order: the stacked layout places the figure by source order.
 */
export function DonutLegend({
  slices,
  signs,
}: {
  slices: readonly DonutSlice[]
  signs?: SignConvention
}) {
  return (
    <ul className="legend legend--wrap">
      {slices.map((slice) => (
        <DonutLegendRow
          key={slice.key}
          slice={slice}
          figure={<Money value={slice.value} signs={signs} tone="neutral" />}
        />
      ))}
    </ul>
  )
}

/** A swatch, a label and a figure of the caller's choosing, for a slice's row in any legend. */
export function DonutLegendRow({ slice, figure }: { slice: DonutSlice; figure: ReactNode }) {
  return (
    <li {...useSliceRowProps(slice, 'legend__row')}>
      <span className="legend__swatch" style={{ background: slice.color }} />
      <span className="legend__label">{slice.label}</span>
      {figure}
    </li>
  )
}

/** The denser legend row, name and value stacked on either end of the line. */
export function AggLegendRow({
  slice,
  label = slice.label,
  figure,
}: {
  slice: DonutSlice
  label?: ReactNode
  figure: ReactNode
}) {
  return (
    <div {...useSliceRowProps(slice, 'agg-legend__row')}>
      <span className="agg-legend__name">
        <span className="agg-legend__swatch" style={{ background: slice.color }} />
        {label}
      </span>
      <span className="agg-legend__value">{figure}</span>
    </div>
  )
}
