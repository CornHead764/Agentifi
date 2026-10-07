/** The month calendar, with a dot per occurrence coloured by direction in the money colours. */

import { clsx } from 'clsx'
import { ChevronLeft, ChevronRight } from 'lucide-react'

import { IconButton, Spinner } from '@/components/ui'
import { calendarGrid } from '@/lib/dateRanges'
import { formatDate, toIsoDate } from '@/lib/format'
import { autopayNote, occurrenceKey, type Occurrence } from '@/lib/clients/upcoming'

const WEEK_HEADS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']

export function MonthCalendar({
  month,
  onMonth,
  occurrences,
  loading = false,
  today = new Date(),
}: {
  /** Any day inside the month being shown. */
  month: Date
  onMonth: (next: Date) => void
  occurrences: readonly Occurrence[]
  /** The occurrences are the last month's, held until this one's arrive. */
  loading?: boolean
  today?: Date
}) {
  const first = new Date(month.getFullYear(), month.getMonth(), 1)

  const byDay = new Map<string, Occurrence[]>()
  for (const occurrence of occurrences) {
    const bucket = byDay.get(occurrence.due_on)
    if (bucket) bucket.push(occurrence)
    else byDay.set(occurrence.due_on, [occurrence])
  }

  const cells = calendarGrid(month)

  const todayIso = toIsoDate(today)

  return (
    <div className="calendar">
      <header className="calendar__head">
        <h3>{formatDate(toIsoDate(first), 'monthLong')}</h3>
        {loading ? <Spinner label="Loading the month" /> : null}
        <span className="toolbar__spacer" />
        <button type="button" className="hint" onClick={() => onMonth(new Date(today))}>
          Today
        </button>
        <IconButton
          label="Previous month"
          variant="ghost"
          size="sm"
          onClick={() => onMonth(new Date(month.getFullYear(), month.getMonth() - 1, 1))}
        >
          <ChevronLeft size={14} />
        </IconButton>
        <IconButton
          label="Next month"
          variant="ghost"
          size="sm"
          onClick={() => onMonth(new Date(month.getFullYear(), month.getMonth() + 1, 1))}
        >
          <ChevronRight size={14} />
        </IconButton>
      </header>

      <div className={clsx('calendar__grid', loading && 'stale')} role="grid" aria-busy={loading}>
        {WEEK_HEADS.map((head) => (
          <span key={head} className="calendar__weekday" role="columnheader">
            {head}
          </span>
        ))}

        {cells.map((date) => {
          const iso = toIsoDate(date)
          const events = byDay.get(iso) ?? []
          return (
            <div
              key={iso}
              role="gridcell"
              className={clsx(
                'calendar__cell',
                date.getMonth() !== first.getMonth() && 'calendar__cell--outside',
                iso === todayIso && 'calendar__cell--today',
              )}
            >
              <span className="calendar__day">{date.getDate()}</span>
              <span className="calendar__dots">
                {events.slice(0, 4).map((event) => (
                  <span
                    key={occurrenceKey(event)}
                    className={clsx(
                      'calendar__dot',
                      event.amount > 0 ? 'calendar__dot--in' : 'calendar__dot--out',
                    )}
                    title={[event.label, formatDate(event.due_on), autopayNote(event)]
                      .filter(Boolean)
                      .join(' · ')}
                  />
                ))}
              </span>
            </div>
          )
        })}
      </div>

      {/* The key: a two-pixel mark means nothing unless the grid says what. */}
      <p className="calendar__key">
        <span className="calendar__dot calendar__dot--in" aria-hidden="true" /> money in
        <span className="calendar__dot calendar__dot--out" aria-hidden="true" /> money out
      </p>
    </div>
  )
}
