import { ChevronLeft, ChevronRight } from 'lucide-react'
import { Bar, BarChart, CartesianGrid, Cell, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import {
  ACCENT_COLOR,
  AXIS,
  BAR_CURSOR,
  ChartTooltip,
  EXPENSE_COLOR,
  GRID,
  hoveredRow,
  INCOME_COLOR,
  useMoneyTick,
} from '@/components/charts'
import { Card, IconButton } from '@/components/ui'
import type { SpendingGrain, SpendingReportData } from '@/lib/clients/reports'
import { chartBars, dayRange, periodTitle, type ChartBar } from '@/lib/reports/spending'
import { scaled } from '@/lib/scale'

/**
 * Net spending per period from a zero baseline, the selected one lit and the
 * one still running starred: spending stands above the axis and a period
 * whose credits outweigh its spending hangs below it in the income colour. A
 * bar is how a period is chosen; the arrows choose by keyboard.
 */
export function PeriodChart({
  data,
  grain,
  onSelect,
}: {
  data: SpendingReportData
  grain: SpendingGrain
  /** Any day of the chosen period. */
  onSelect: (from: string) => void
}) {
  const tick = useMoneyTick()
  const bars = chartBars(data.periods, grain)
  const at = bars.findIndex((bar) => bar.key === data.period.key)
  const step = (offset: number) => {
    const next = bars[at + offset]
    if (next) onSelect(next.from)
  }

  return (
    <Card
      title={periodTitle(data.period, grain)}
      subtitle={`${dayRange(data.period.from, data.period.through)}${data.period.partial ? '*' : ''}`}
      actions={
        <>
          <IconButton
            label="Previous period"
            variant="ghost"
            size="sm"
            disabled={at <= 0}
            onClick={() => step(-1)}
          >
            <ChevronLeft size={14} />
          </IconButton>
          <IconButton
            label="Next period"
            variant="ghost"
            size="sm"
            disabled={at < 0 || at >= bars.length - 1}
            onClick={() => step(1)}
          >
            <ChevronRight size={14} />
          </IconButton>
        </>
      }
    >
      <ResponsiveContainer width="100%" height={scaled(200)}>
        <BarChart data={bars} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
          <CartesianGrid {...GRID} vertical={false} />
          <XAxis dataKey="label" {...AXIS} />
          <YAxis tickFormatter={tick} width={scaled(56)} {...AXIS} />
          <ReferenceLine y={0} stroke="var(--border-strong)" />
          <Tooltip
            cursor={BAR_CURSOR}
            content={({ payload }) => {
              const bar = hoveredRow<ChartBar>(payload)
              if (!bar) return null
              return (
                <ChartTooltip
                  title={periodTitle(bar.period, grain)}
                  rows={[
                    { label: 'Income', value: bar.period.income, color: INCOME_COLOR },
                    bar.credit
                      ? { label: 'Net credit', value: bar.period.spent, color: INCOME_COLOR }
                      : { label: 'Total spent', value: bar.period.spent, color: EXPENSE_COLOR },
                    { label: 'Remaining', value: bar.period.remaining },
                  ]}
                  hint="Click to view breakdown"
                />
              )
            }}
          />
          <Bar
            dataKey="plot"
            radius={[3, 3, 0, 0]}
            isAnimationActive={false}
            className="spend-chart__bar"
            onClick={(_: unknown, index: number) => {
              const bar = bars[index]
              if (bar) onSelect(bar.from)
            }}
          >
            {bars.map((bar) => (
              <Cell
                key={bar.key}
                fill={bar.credit ? INCOME_COLOR : ACCENT_COLOR}
                fillOpacity={bar.key === data.period.key ? 1 : 0.4}
              />
            ))}
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </Card>
  )
}
