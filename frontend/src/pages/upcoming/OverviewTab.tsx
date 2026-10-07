import { useMemo, useState } from 'react'

import { Stat } from '@/components/figures'
import { Money } from '@/components/Money'
import { QueryBoundary } from '@/components/QueryBoundary'
import { ReminderList } from '@/components/ReminderList'
import { Card, EmptyState, Switch } from '@/components/ui'
import { isPayManually, useOccurrences } from '@/lib/clients/upcoming'
import { calendarGrid } from '@/lib/dateRanges'
import { toIsoDate } from '@/lib/format'
import { pastDueWindow } from '@/lib/transactions/reminders'
import type { Horizon } from '@/lib/upcoming/horizon'

import { MonthCalendar } from './MonthCalendar'
import { SkipAllPastDue } from './SkipAllPastDue'

/** Every day the month calendar draws. */
function calendarWindow(month: Date): { from: string; to: string } {
  const grid = calendarGrid(month)
  return { from: toIsoDate(grid[0]), to: toIsoDate(grid[grid.length - 1]) }
}

export function OverviewTab({ horizon }: { horizon: Horizon }) {
  const [month, setMonth] = useState(() => new Date())
  const [showPaid, setShowPaid] = useState(false)
  const bounds = horizon
  const occurrences = useOccurrences(bounds.from, bounds.to)
  // A second window rather than the first one widened: the settled slots
  // belong in the list, not in the Income, Expenses and Net above it.
  const withPaid = useOccurrences(bounds.from, bounds.to, {
    includeFulfilled: true,
    enabled: showPaid,
  })
  const reminders = showPaid ? withPaid : occurrences
  // What is still owed from before the horizon, as a window of its own: widening
  // the horizon would fold history into the Income, Expenses and Net above.
  const behind = useMemo(() => pastDueWindow(), [])
  const overdue = useOccurrences(behind.from, behind.to)
  // The calendar pages independently of the horizon, so it asks for its own
  // window; the horizon list would draw an empty grid one month over.
  const grid = useMemo(() => calendarWindow(month), [month])
  const inMonth = useOccurrences(grid.from, grid.to, { keepPrevious: true })

  return (
    // The horizon's totals run the width of the page, as the account header
    // does above the register.
    <div className="stack">
      <Card>
        <QueryBoundary query={occurrences} rows={4}>
          {(data) => (
            <>
              <div className="stat-row">
                <Stat label="Income">
                  <Money value={data.summary.income} showPlus />
                </Stat>
                <Stat label="Expenses">
                  <Money value={data.summary.expenses} signs="absolute" tone="neutral" />
                </Stat>
                <Stat label="Net">
                  <Money value={data.summary.net} />
                </Stat>
              </div>
              {(overdue.data?.summary.past_due ?? 0) > 0 ? (
                // A link, because the card that lists these sits far below in
                // the other column.
                <p className="muted">
                  <a href="#past-due">
                    Past due: {overdue.data?.summary.past_due}
                  </a>{' '}
                  · Scheduled: {data.summary.count}
                </p>
              ) : null}
            </>
          )}
        </QueryBoundary>
      </Card>

      <div className="split split--upcoming">
        <Card
          title="Reminders"
          flush
          actions={
            <Switch
              label="Show paid"
              labelPosition="before"
              checked={showPaid}
              onCheckedChange={setShowPaid}
            />
          }
        >
          <QueryBoundary query={reminders} rows={8}>
            {(data) => <ReminderList occurrences={data.items} />}
          </QueryBoundary>
        </Card>

        <div className="stack">
          <Card flush>
            <QueryBoundary query={inMonth} rows={6}>
              {(data) => (
                <MonthCalendar
                  month={month}
                  onMonth={setMonth}
                  occurrences={data.items}
                  loading={inMonth.isPlaceholderData}
                />
              )}
            </QueryBoundary>
          </Card>

          <QueryBoundary query={overdue} rows={4}>
            {(data) => {
              const past = data.items.filter((one) => one.status === 'past_due')
              // A statement is marked paid, never skipped.
              const skippable = past.filter((one) => !isPayManually(one))
              return (
                <Card
                  id="past-due"
                  title="All past due"
                  flush
                  // Skipping is the bulk action and marking paid is not: a
                  // bulk mark-paid would write amounts nobody checked.
                  actions={
                    skippable.length > 1 ? <SkipAllPastDue occurrences={skippable} /> : undefined
                  }
                >
                  {past.length === 0 ? (
                    <EmptyState compact title="Nothing past due." />
                  ) : (
                    <ReminderList occurrences={past} />
                  )}
                </Card>
              )
            }}
          </QueryBoundary>
        </div>
      </div>
    </div>
  )
}
