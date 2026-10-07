/** The Date control's two-month calendar; the range grammar lives in `pickRangeDay`. */

import { clsx } from 'clsx'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { useState } from 'react'

import { IconButton } from '@/components/ui'
import { formatMonthKey, toIsoDate } from '@/lib/format'
import type { DateRange } from '@/lib/dateRanges'

const WEEK_HEADS = ['S', 'M', 'T', 'W', 'T', 'F', 'S']


function monthCells(year: number, month: number): (string | null)[] {
  const first = new Date(year, month, 1)
  const days = new Date(year, month + 1, 0).getDate()
  const lead = first.getDay()
  const cells: (string | null)[] = Array.from({ length: lead }, () => null)
  for (let day = 1; day <= days; day++) {
    cells.push(toIsoDate(new Date(year, month, day)))
  }
  return cells
}

function MonthGrid({
  year,
  month,
  range,
  onPick,
}: {
  year: number
  month: number
  range: DateRange
  onPick: (day: string) => void
}) {
  const inRange = (day: string) =>
    range.from !== null && range.to !== null && day > range.from && day < range.to

  return (
    <div className="range-cal__month">
      <p className="range-cal__title">{formatMonthKey(toIsoDate(new Date(year, month, 1)).slice(0, 7))}</p>
      <div className="range-cal__grid" role="grid">
        {WEEK_HEADS.map((head, index) => (
          <span key={`${head}-${index}`} className="range-cal__head" aria-hidden="true">
            {head}
          </span>
        ))}
        {monthCells(year, month).map((day, index) =>
          day === null ? (
            <span key={`pad-${index}`} />
          ) : (
            <button
              key={day}
              type="button"
              className={clsx('range-cal__day', {
                'range-cal__day--edge': day === range.from || day === range.to,
                'range-cal__day--in': inRange(day),
              })}
              aria-pressed={day === range.from || day === range.to}
              onClick={() => onPick(day)}
            >
              {Number(day.slice(8))}
            </button>
          ),
        )}
      </div>
    </div>
  )
}

export function RangeCalendar({
  range,
  onPick,
  today = new Date(),
}: {
  range: DateRange
  onPick: (day: string) => void
  today?: Date
}) {
  // Opens on the picked start's month, or on last month so that "the recent
  // window" — the common case — is on screen without paging back.
  const [lead, setLead] = useState(() => {
    const anchor =
      range.from === null
        ? new Date(today.getFullYear(), today.getMonth() - 1, 1)
        : new Date(Number(range.from.slice(0, 4)), Number(range.from.slice(5, 7)) - 1, 1)
    return anchor
  })

  const shift = (by: number) =>
    setLead((current) => new Date(current.getFullYear(), current.getMonth() + by, 1))

  return (
    <div className="range-cal">
      <IconButton label="Earlier months" variant="ghost" size="sm" onClick={() => shift(-1)}>
        <ChevronLeft size={14} />
      </IconButton>
      <MonthGrid year={lead.getFullYear()} month={lead.getMonth()} range={range} onPick={onPick} />
      <MonthGrid
        year={lead.getFullYear()}
        month={lead.getMonth() + 1}
        range={range}
        onPick={onPick}
      />
      <IconButton label="Later months" variant="ghost" size="sm" onClick={() => shift(1)}>
        <ChevronRight size={14} />
      </IconButton>
    </div>
  )
}
