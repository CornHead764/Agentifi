/**
 * Frequency & Occurrence. The picker writes a `Recurrence` and never a rule
 * type of its own: *Twice a month* is `byMonthDay: [1, 15]` (one series, two
 * occurrences), and *Multiple fixed dates* is the same field with more.
 *
 * For a monthly rule, *Day of the Month / Day of the Week* populates
 * `byMonthDay` or `byDay`, never both.
 *
 * *Active months* limits a daily, weekly or monthly rule to part of the year,
 * as lawn care runs April to October. None picked is every month.
 */

import { X } from 'lucide-react'

import { Button, Callout, ChipGroup, Field, IconButton, Input, OptionSelect } from '@/components/ui'
import { formatDate, ordinal, toIsoDate } from '@/lib/format'
import {
  FREQUENCY_OPTIONS,
  FREQUENCY_OPTION_LABELS,
  MONTH_NAMES,
  WEEKDAYS,
  WEEKDAY_NAMES,
  describeRecurrence,
  optionFor,
  previewOccurrences,
  reanchorRecurrence,
  recurrenceFor,
  takesActiveMonths,
  takesInterval,
  takesMonthDays,
  type FrequencyOption,
  type Recurrence,
  type Weekday,
} from '@/lib/recurrence'
import { toggled } from '@/lib/toggle'

export interface SchedulePatch {
  recurrence?: Recurrence
  startOn?: string
  endOn?: string | null
}

export function SeriesSchedule({
  recurrence,
  startOn,
  endOn,
  onChange,
}: {
  recurrence: Recurrence
  startOn: string
  endOn: string | null
  onChange: (patch: SchedulePatch) => void
}) {
  const option = optionFor(recurrence)
  const anchor = new Date(`${startOn}T00:00:00`)
  const byWeekday = recurrence.by_day.length > 0

  // The active months carry over a change of frequency or day; a frequency
  // that takes none drops them.
  const set = (next: FrequencyOption, overrides?: Parameters<typeof recurrenceFor>[2]) => {
    onChange({
      recurrence: recurrenceFor(next, anchor, { byMonth: recurrence.by_month, ...overrides }),
    })
  }

  return (
    <div className="freq">
      <Field label="Frequency">
        <OptionSelect
          value={option}
          onValueChange={(value) => set(asOption(value))}
          options={FREQUENCY_OPTIONS.map((one) => ({
            value: one,
            label: FREQUENCY_OPTION_LABELS[one],
          }))}
        />
      </Field>

      <div className="freq__dates">
        <Field label="Start date">
          <Input
            type="date"
            value={startOn}
            onChange={(event) => {
              const next = event.target.value
              if (!next) return
              onChange({
                startOn: next,
                recurrence: reanchorRecurrence(recurrence, anchor, new Date(`${next}T00:00:00`)),
              })
            }}
          />
        </Field>
        <Field label="End date" hint="Leave empty if it has no end.">
          <Input
            type="date"
            value={endOn ?? ''}
            onChange={(event) => onChange({ endOn: event.target.value || null })}
          />
        </Field>
      </div>

      {takesInterval(option) ? (
        <Field label={FREQUENCY_OPTION_LABELS[option].replace('X', 'how many')}>
          <Input
            type="number"
            min={1}
            numeric
            value={recurrence.interval}
            onChange={(event) =>
              set(option, { interval: Math.max(1, Number(event.target.value) || 1) })
            }
          />
        </Field>
      ) : null}

      {option === 'EVERY_MONTH' ? (
        <ChipGroup
          label="Monthly anchor"
          value={byWeekday ? 'weekday' : 'monthday'}
          options={[
            { value: 'monthday', label: 'Day of the Month' },
            { value: 'weekday', label: 'Day of the Week' },
          ]}
          onChange={(anchorOn) =>
            anchorOn === 'weekday'
              ? set('EVERY_MONTH', { byDay: [WEEKDAYS[(anchor.getDay() + 6) % 7]] })
              : set('EVERY_MONTH', { byMonthDay: [anchor.getDate()] })
          }
        />
      ) : null}

      {takesMonthDays(option) && !byWeekday ? (
        <MonthDayChips
          days={recurrence.by_month_day}
          onChange={(days) => set(option, { byMonthDay: days })}
        />
      ) : null}

      {byWeekday && option === 'EVERY_MONTH' ? (
        <WeekdayChips
          days={recurrence.by_day}
          onChange={(days) => set('EVERY_MONTH', { byDay: days })}
        />
      ) : null}

      {option === 'EVERY_WEEK' || option === 'EVERY_X_WEEKS' ? (
        <WeekdayChips days={recurrence.by_day} onChange={(days) => set(option, { byDay: days })} />
      ) : null}

      {takesActiveMonths(option) ? (
        <MonthToggles
          months={recurrence.by_month}
          onChange={(months) => set(option, { byMonth: months })}
        />
      ) : null}

      <Callout className="freq__preview">
        <p>{describeRecurrence(recurrence)}</p>
        <details>
          <summary>Preview next occurrence</summary>
          <ul>
            {previewOccurrences(recurrence, anchor).map((date) => (
              <li key={toIsoDate(date)}>{formatDate(toIsoDate(date))}</li>
            ))}
          </ul>
        </details>
      </Callout>
    </div>
  )
}

function asOption(value: string): FrequencyOption {
  return FREQUENCY_OPTIONS.find((one) => one === value) ?? 'EVERY_MONTH'
}

/**
 * A multi-select of month days, as removable chips. "The last day" is stored
 * as -1 rather than 31, so it lands on the end of February.
 */
function MonthDayChips({
  days,
  onChange,
}: {
  days: readonly number[]
  onChange: (days: number[]) => void
}) {
  const remaining = Array.from({ length: 31 }, (_, index) => index + 1).filter(
    (day) => !days.includes(day),
  )

  return (
    <Field label="Select one or more days in the month" as="group">
      <div className="chips chips--wrap">
        {[...days]
          .sort((a, b) => a - b)
          .map((day) => (
            <span key={day} className="chip chip--on chip--removable">
              {day < 0 ? 'Last day' : ordinal(day)}
              <IconButton
                size="sm"
                variant="ghost"
                label={`Remove ${day < 0 ? 'last day' : ordinal(day)}`}
                onClick={() => onChange(days.filter((one) => one !== day))}
              >
                <X size={11} />
              </IconButton>
            </span>
          ))}
      </div>

      <OptionSelect
        value=""
        onValueChange={(value) => onChange([...days, Number(value)])}
        placeholder="Add a day"
        options={[
          ...(days.includes(-1) ? [] : [{ value: '-1', label: 'Last day' }]),
          ...remaining.map((day) => ({ value: String(day), label: ordinal(day) })),
        ]}
      />
    </Field>
  )
}

function MonthToggles({
  months,
  onChange,
}: {
  months: readonly number[]
  onChange: (months: number[]) => void
}) {
  return (
    <Field
      label="Active months"
      as="group"
      hint="Only the months it happens in, such as April to October for lawn care. None picked is every month."
    >
      <div className="chips chips--wrap">
        {MONTH_NAMES.map((name, index) => {
          const month = index + 1
          const on = months.includes(month)
          return (
            <Button
              key={name}
              size="sm"
              variant={on ? 'primary' : 'secondary'}
              aria-pressed={on}
              aria-label={name}
              onClick={() => onChange(toggled(months, month, !on))}
            >
              {name.slice(0, 3)}
            </Button>
          )
        })}
      </div>
    </Field>
  )
}

function WeekdayChips({
  days,
  onChange,
}: {
  days: readonly Weekday[]
  onChange: (days: Weekday[]) => void
}) {
  return (
    <Field label="Select one or more days of the week" as="group">
      <div className="chips chips--wrap">
        {WEEKDAYS.map((code) => {
          const on = days.includes(code)
          return (
            <Button
              key={code}
              size="sm"
              variant={on ? 'primary' : 'secondary'}
              aria-pressed={on}
              onClick={() => onChange(toggled(days, code, !on))}
            >
              {WEEKDAY_NAMES[code].slice(0, 3)}
            </Button>
          )
        })}
      </div>
    </Field>
  )
}
