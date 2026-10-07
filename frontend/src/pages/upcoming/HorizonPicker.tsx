/**
 * *Next 30 days* / *Select dates*. The date pair applies only once both halves
 * are filled; applying on the first keystroke would refetch against the year
 * 0002.
 */

import { CalendarDays, ChevronDown } from 'lucide-react'
import { useState } from 'react'

import {
  Button,
  Field,
  Input,
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui'
import {
  HORIZON_DAYS,
  customHorizon,
  horizonLabel,
  horizonOf,
  isCompleteRange,
  type Horizon,
} from '@/lib/upcoming/horizon'

export function HorizonPicker({
  value,
  onChange,
}: {
  value: Horizon
  onChange: (horizon: Horizon) => void
}) {
  const [open, setOpen] = useState(false)
  const [from, setFrom] = useState(value.days === null ? value.from : '')
  const [to, setTo] = useState(value.days === null ? value.to : '')

  const apply = (nextFrom: string, nextTo: string) => {
    setFrom(nextFrom)
    setTo(nextTo)
    if (isCompleteRange(nextFrom, nextTo)) onChange(customHorizon(nextFrom, nextTo))
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button size="sm" variant={value.days === null ? 'primary' : 'secondary'}>
          <CalendarDays size={13} /> {horizonLabel(value)} <ChevronDown size={13} />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="picker" align="start">
        <div className="option-list">
          {HORIZON_DAYS.map((days) => (
            <div key={days} className="option-list__row">
              <Button
                variant="ghost"
                onClick={() => {
                  onChange(horizonOf(days))
                  setOpen(false)
                }}
              >
                Next {days} days
              </Button>
            </div>
          ))}
        </div>

        <Field label="Select dates" as="group">
          <div className="form-row">
            <Input
              type="date"
              aria-label="From"
              value={from}
              onChange={(event) => apply(event.target.value, to)}
            />
            <Input
              type="date"
              aria-label="To"
              value={to}
              onChange={(event) => apply(from, event.target.value)}
            />
          </div>
        </Field>
      </PopoverContent>
    </Popover>
  )
}
