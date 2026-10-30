import { useMemo } from 'react'

import { MoneyOrDash, PercentText } from '@/components/figures'
import { QueryBoundary } from '@/components/QueryBoundary'
import { List, ListRow } from '@/components/ui'
import { useSpendingPlanMonth, useSpendingPlanMonths } from '@/lib/clients/dashboard'
import { plural, monthKey, formatMonthKey } from '@/lib/format'
import type { Money } from '@/lib/money'
import {
  completedMonthsBefore,
  savingsFigures,
  trailingSavingsRate,
  type SpendingPlanMonth,
} from '@/lib/spendingPlan'

/** How many completed months the trailing figure covers. */
const TRAILING_MONTHS = 3

/**
 * The share of this month's income still unspent, where the month is on
 * course to end, and whether either is normal for this household.
 *
 * The projection leads, since the rate to date is flattered early in the
 * month. Then the amounts behind it, last month, and the last three completed
 * months: total saved over total earned, not the average of their rates
 * (`trailingSavingsRate`).
 *
 * A month with no income has no savings rate, not a rate of zero, so its
 * percentages render as a dash.
 */
export function SavingsRatePanel() {
  const month = useSpendingPlanMonth()
  // Frozen at first run: recomputing per render would roll the window over at
  // midnight on the 1st while the cached months beneath it stayed put.
  const priorKeys = useMemo(() => completedMonthsBefore(monthKey(), TRAILING_MONTHS), [])
  const prior = useSpendingPlanMonths(priorKeys)

  // In the order asked for, so the first is last month. The trailing rate
  // waits for all of them rather than covering a window nothing names.
  const completed = prior.map((query) => query.data)
  const lastMonth = completed[0]
  const allLoaded = completed.every((one): one is SpendingPlanMonth => one !== undefined)
  const trailing = allLoaded ? trailingSavingsRate(completed) : null

  return (
    <QueryBoundary query={month} rows={5}>
      {(data) => {
        const figures = savingsFigures(data)
        return (
          <div>
            <p className="widget__lead">On course for</p>
            <p className="widget__headline">
              <PercentText rate={asPercent(figures.projectedRate)} digits={0} tone="signed" />
            </p>
            <p className="muted">
              <PercentText rate={asPercent(figures.rate)} digits={0} tone="neutral" /> so far &middot;{' '}
              {plural(data.days_remaining, 'day')} left
            </p>

            <List>
              <Line label="Income" value={figures.income} />
              {/* Not "spent": Bills is the month's whole obligation and Planned
                  Spend a target rather than a receipt. */}
              <Line label="Spent & committed" value={figures.committed} />
              <Line label="Left" value={figures.saved} signed />
              <Line
                label="Last month"
                note={lastMonth === undefined ? undefined : formatMonthKey(lastMonth.month, 'month')}
                rate={lastMonth === undefined ? null : savingsFigures(lastMonth).rate}
              />
              <Line
                label={`Last ${TRAILING_MONTHS} months`}
                note={allLoaded ? windowLabel(priorKeys) : undefined}
                rate={trailing?.rate ?? null}
              />
            </List>
          </div>
        )
      }}
    </QueryBoundary>
  )
}

/**
 * One line of the card: a label with an amount or a percentage. Amounts read
 * absolute, apart from what is left, where the sign is the point.
 */
function Line({
  label,
  note,
  value,
  rate,
  signed = false,
}: {
  label: string
  note?: string
  value?: Money
  rate?: number | null
  signed?: boolean
}) {
  return (
    <ListRow
      title={label}
      sub={note}
      figures={
        value === undefined ? (
          <PercentText rate={asPercent(rate ?? null)} digits={0} tone="signed" />
        ) : (
          <MoneyOrDash
            value={value}
            signs={signed ? 'stored' : 'absolute'}
            tone={signed ? 'signed' : 'neutral'}
          />
        )
      }
    />
  )
}

/** `PercentText` takes percent units; a savings rate is a plain share. */
function asPercent(rate: number | null): number | null {
  return rate === null ? null : rate * 100
}

/** "Jun – Aug", from the months the trailing figure actually covered. */
function windowLabel(months: readonly string[]): string {
  const oldest = months[months.length - 1]
  const newest = months[0]
  return `${formatMonthKey(oldest, 'month')} – ${formatMonthKey(newest, 'month')}`
}
