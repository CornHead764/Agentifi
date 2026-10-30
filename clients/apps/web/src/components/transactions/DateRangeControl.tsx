import { CalendarDays } from 'lucide-react'
import { useRef, useState, type KeyboardEvent } from 'react'

import { RangeCalendar } from '@/components/transactions/RangeCalendar'
import {
  Button,
  DialogActions,
  Field,
  Input,
  Popover,
  PopoverContent,
  PopoverTrigger,
  Sheet,
  SheetContent,
  SheetTrigger,
} from '@/components/ui'
import { nextChipIndex } from '@/components/ui/chip-keys'
import {
  ALL_TIME_SELECTION,
  DATE_PRESETS,
  labelForSelection,
  selectionForToken,
  shortLabelForSelection,
  type DateRange,
  type DateSelection,
} from '@/lib/dateRanges'
import { NARROW_QUERY } from '@/lib/transactions/columns'
import { pickRangeDay } from '@/lib/transactions/dates'
import { useMediaQuery } from '@/lib/useMediaQuery'

/**
 * The Date control: presets beside a custom window over a two-month calendar.
 * A preset stores its token and the two dates it resolved to, so the register
 * and the account summary are asked for the same window even across midnight.
 * A phone gets a short button and the picker in a sheet: a popover that tall
 * would not fit above the keyboard. The sheet has no calendar; the phone's own
 * date picker opens from each field.
 */
export function DateRangeControl({
  value,
  onChange,
}: {
  value: DateSelection
  onChange: (value: DateSelection) => void
}) {
  const narrow = useMediaQuery(NARROW_QUERY)
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState<DateRange>(value.range)
  const list = useRef<HTMLDivElement>(null)

  // Opening lands on the chosen preset; a custom window has none and keeps
  // the default, the first option.
  const focusSelected = (event: Event) => {
    const selected = list.current?.querySelector<HTMLElement>(
      '.picker__option[data-selected="true"]',
    )
    if (!selected) return
    event.preventDefault()
    selected.focus()
  }

  const onListKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const all = [...event.currentTarget.querySelectorAll<HTMLElement>('.picker__option')]
    const at = all.findIndex((option) => option === document.activeElement)
    if (at === -1) return
    const next = nextChipIndex(event.key, at, all.length)
    if (next === null) return
    event.preventDefault()
    all[next]?.focus()
  }

  const choose = (preset: string) => {
    const selection = selectionForToken(preset)
    if (selection === null) return
    onChange(selection)
    setOpen(false)
  }

  const onOpenChange = (next: boolean) => {
    setOpen(next)
    if (next) setDraft(value.range)
  }

  const label = labelForSelection(value)
  const trigger = (
    <Button
      size="sm"
      variant={value.preset !== null || value.range.from !== null ? 'primary' : 'secondary'}
      title={narrow ? label : undefined}
    >
      <CalendarDays size={13} /> {narrow ? shortLabelForSelection(value) : label}
    </Button>
  )

  const picker = (
    <>
      <div className="picker__list" ref={list} onKeyDown={onListKeyDown}>
        <button
          type="button"
          className="picker__option"
          data-selected={value.preset === null && value.range.from === null}
          aria-pressed={value.preset === null && value.range.from === null}
          onClick={() => {
            onChange(ALL_TIME_SELECTION)
            setOpen(false)
          }}
        >
          All time
        </button>
        {DATE_PRESETS.map((preset) => (
          <button
            key={preset.id}
            type="button"
            className="picker__option"
            data-selected={value.preset === preset.id}
            aria-pressed={value.preset === preset.id}
            onClick={() => choose(preset.id)}
          >
            {preset.label}
          </button>
        ))}
      </div>

      <div className="picker__custom">
        <div className="picker__fields">
          <Field label="Start date">
            <Input
              type="date"
              value={draft.from ?? ''}
              onChange={(event) =>
                setDraft((current) => ({ ...current, from: event.target.value || null }))
              }
            />
          </Field>
          <Field label="End date">
            <Input
              type="date"
              value={draft.to ?? ''}
              onChange={(event) =>
                setDraft((current) => ({ ...current, to: event.target.value || null }))
              }
            />
          </Field>
        </div>

        {narrow ? null : (
          <RangeCalendar range={draft} onPick={(day) => setDraft(pickRangeDay(draft, day))} />
        )}

        <div className="picker__apply">
          <DialogActions onCancel={() => setOpen(false)}>
            <Button
              variant="primary"
              disabled={draft.from === null && draft.to === null}
              onClick={() => {
                onChange({ range: draft, preset: null })
                setOpen(false)
              }}
            >
              Apply dates
            </Button>
          </DialogActions>
        </div>
      </div>
    </>
  )

  if (narrow) {
    return (
      <Sheet open={open} onOpenChange={onOpenChange}>
        <SheetTrigger asChild>{trigger}</SheetTrigger>
        <SheetContent title="Dates" onOpenAutoFocus={focusSelected}>
          <div className="sheet__body picker picker--range picker--sheet">{picker}</div>
        </SheetContent>
      </Sheet>
    )
  }

  return (
    <Popover open={open} onOpenChange={onOpenChange}>
      <PopoverTrigger asChild>{trigger}</PopoverTrigger>
      <PopoverContent className="picker picker--range" onOpenAutoFocus={focusSelected}>
        {picker}
      </PopoverContent>
    </Popover>
  )
}
