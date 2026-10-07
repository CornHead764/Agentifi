import { ChartColumnBig, ChartPie, ChevronDown } from 'lucide-react'
import { useMemo } from 'react'
import {
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  ResponsiveContainer,
  Tooltip as RechartsTooltip,
  XAxis,
  YAxis,
} from 'recharts'

import {
  AggLegendRow,
  AXIS,
  BAR_CURSOR,
  ChartTooltip,
  Donut,
  DonutGroup,
  DrillTrail,
  GRID,
  type DrillCrumb,
  seriesColor,
  useMoneyTick,
  type DonutSlice,
} from '@/components/charts'
import { MoneyOrDash } from '@/components/figures'
import { Money } from '@/components/Money'
import {
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
  EmptyState,
  IconButton,
} from '@/components/ui'
import {
  collapseTail,
  monthSeries,
  type Direction,
  type GroupBy,
  type TransactionAggregate,
} from '@/lib/transactions/aggregate'
import { formatDate } from '@/lib/format'
import { stepFor, type DrillStep } from '@/lib/transactions/drill'
import type { Uuid } from '@/lib/transactions/types'
import { scaled } from '@/lib/scale'

function monthOf(value: unknown): string | null {
  if (typeof value === 'object' && value !== null && 'month' in value &&
      typeof value.month === 'string') {
    return value.month
  }
  return null
}

export type ChartShape = 'total' | 'over-time'

export interface AggregateViewProps {
  direction: Direction
  shape: ChartShape
  /** `GET /transactions/aggregate`, or undefined while it is in flight. */
  data: TransactionAggregate | undefined
  from: string | null
  to: string | null
  /** The dimension the ranking is over, which is what a clicked wedge narrows. */
  groupBy: GroupBy
  /** The category the chart is drilled into, whose own line is not a step further. */
  under: Uuid | null
  /** The levels drilled through, for the breadcrumb; empty at the top. */
  trail: readonly DrillCrumb[]
  onGroupByChange: (groupBy: GroupBy) => void
  onShapeChange: (shape: ChartShape) => void
  /** A clicked wedge, one level further in. */
  onDrill: (step: DrillStep) => void
  /** Back out to a breadcrumb: 0 is the top. */
  onStep: (depth: number) => void
}

/**
 * The Spending and Income tabs' chart. Every figure comes from one aggregate
 * over the whole match set, taken with the register's own window, filter and
 * account scope (trap 5), never summed from loaded register pages.
 */
export function AggregateView({
  direction,
  shape,
  data,
  from,
  to,
  groupBy,
  under,
  trail,
  onGroupByChange,
  onShapeChange,
  onDrill,
  onStep,
}: AggregateViewProps) {
  const buckets = useMemo(() => data?.buckets ?? [], [data])
  const ranked = useMemo(() => collapseTail(buckets, 7), [buckets])
  const months = useMemo(
    () => (shape === 'over-time' ? monthSeries(data?.months ?? [], ranked, direction) : []),
    [shape, data, ranked, direction],
  )
  const moneyTick = useMoneyTick()

  // A slice is drawn from the magnitude; the legend reports the signed
  // figure. `stepFor` decides whether a wedge can narrow the register.
  const slices: DonutSlice[] = ranked.map((bucket, index) => ({
    key: bucket.key,
    label: bucket.label,
    value: bucket.total,
    color: seriesColor(index),
    action: stepFor(groupBy, bucket, under) === null ? undefined : `Narrow to ${bucket.label}`,
  }))

  const drill = (slice: DonutSlice) => {
    const step = stepFor(groupBy, slice, under)
    if (step !== null) onDrill(step)
  }

  return (
    <>
      <DrillTrail label="Chart breakdown" root="All" trail={trail} onStep={onStep} />
      <div className="aggregate__headline">
        <div>
          <p className="aggregate__label hint">
            {direction === 'spending' ? 'Total expenses' : 'Total income'}
          </p>
          <p className="figure--total">
            {/* A dash until the figure lands, never $0.00: an empty window and
                a window still loading are different answers. */}
            <MoneyOrDash
              value={data?.total ?? null}
              signs="absolute"
              tone="neutral"
              reason="The total for this window has not loaded."
            />
          </p>
          <p className="aggregate__label hint">
            {from === null ? 'All time' : formatDate(from)}
            {to === null ? '' : ` – ${formatDate(to)}`}
          </p>
        </div>
        <div className="row row--wrap txn-actions">
          <GroupBySelect value={groupBy} onChange={onGroupByChange} />
          <IconButton
            label="Show the total as a donut"
            variant={shape === 'total' ? 'primary' : 'ghost'}
            size="sm"
            onClick={() => onShapeChange('total')}
          >
            <ChartPie size={14} />
          </IconButton>
          <IconButton
            label="Show it over time"
            variant={shape === 'over-time' ? 'primary' : 'ghost'}
            size="sm"
            onClick={() => onShapeChange('over-time')}
          >
            <ChartColumnBig size={14} />
          </IconButton>
        </div>
      </div>

      <div className="aggregate__chart">
        {shape === 'total' ? (
          <>
            <DonutGroup className="aggregate__pair" onSelect={drill}>
              <div className="aggregate__donut">
                <Donut height={220} signs="absolute" slices={slices} />
              </div>
              <div className="agg-legend">
                {slices.map((slice) => (
                  <AggLegendRow
                    key={slice.key}
                    slice={slice}
                    figure={<Money value={slice.value} signs="absolute" tone="neutral" />}
                  />
                ))}
                {data !== undefined && ranked.length === 0 ? (
                  <EmptyState compact title="Nothing in this window." />
                ) : null}
              </div>
            </DonutGroup>
          </>
        ) : (
          <ResponsiveContainer width="100%" height={scaled(280)}>
            <BarChart data={months.map((month) => ({ month: month.label, ...month.values }))}>
              <CartesianGrid {...GRID} vertical={false} />
              <XAxis dataKey="month" {...AXIS} />
              {/* The masked tick: an axis printing plotted dollars would leak
                  them in privacy mode. */}
              <YAxis width={scaled(64)} {...AXIS} tickFormatter={moneyTick} />
              <RechartsTooltip
                cursor={BAR_CURSOR}
                content={({ payload }) => {
                  const hovered = monthOf(payload?.[0]?.payload)
                  const month = hovered === null ? undefined : months.find((one) => one.label === hovered)
                  if (month === undefined) return null
                  return (
                    <ChartTooltip
                      title={month.label}
                      rows={ranked.map((bucket, index) => ({
                        label: bucket.label,
                        value: month.amounts[bucket.key],
                        color: seriesColor(index),
                      }))}
                    />
                  )
                }}
              />
              <Legend wrapperStyle={{ fontSize: 12 }} />
              {ranked.map((bucket, index) => (
                <Bar
                  key={bucket.key}
                  dataKey={bucket.key}
                  name={bucket.label}
                  fill={seriesColor(index)}
                />
              ))}
            </BarChart>
          </ResponsiveContainer>
        )}
      </div>
    </>
  )
}

const GROUP_BY_LABELS: Record<GroupBy, string> = {
  category: 'By category',
  payee: 'By payee',
  tag: 'By tag',
  none: 'By none',
}

function GroupBySelect({
  value,
  onChange,
}: {
  value: GroupBy
  onChange: (value: GroupBy) => void
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button size="sm">
          {GROUP_BY_LABELS[value]} <ChevronDown size={13} />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuRadioGroup value={value} onValueChange={(next) => onChange(readGroupBy(next))}>
          {Object.entries(GROUP_BY_LABELS).map(([id, label]) => (
            <DropdownMenuRadioItem key={id} value={id}>
              {label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function readGroupBy(value: string): GroupBy {
  if (value === 'payee' || value === 'tag' || value === 'none') return value
  return 'category'
}
