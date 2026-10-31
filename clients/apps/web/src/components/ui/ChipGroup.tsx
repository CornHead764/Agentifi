import { clsx } from 'clsx'
import type { KeyboardEvent, ReactNode } from 'react'

import { nextChipIndex } from './chip-keys'
import { Tooltip } from './Tooltip'

export interface ChipOption<T extends string> {
  value: T
  label: ReactNode
  /** Read on hover; the label still has to stand on its own. */
  hint?: ReactNode
  /** For a label that is a glyph ("%"). */
  ariaLabel?: string
}

export interface ChipGroupProps<T extends string> {
  /** Names the choice: "Return measure". */
  label: string
  value: T
  options: readonly ChipOption<T>[]
  onChange: (value: T) => void
  /** `box` draws the options in a tray, `tight` in a slimmer one, `wrap` as loose pills. */
  layout?: 'box' | 'tight' | 'wrap'
  className?: string
}

/**
 * One choice of a few, as a row of chips. A radio group: Tab lands on the
 * chosen chip, and the arrows move the choice.
 */
export function ChipGroup<T extends string>({
  label,
  value,
  options,
  onChange,
  layout = 'box',
  className,
}: ChipGroupProps<T>) {
  const chosen = options.findIndex((option) => option.value === value)
  const focusable = chosen === -1 ? 0 : chosen

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const radios = [...event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="radio"]')]
    const at = radios.findIndex((radio) => radio === document.activeElement)
    const next = nextChipIndex(event.key, at === -1 ? focusable : at, options.length)
    if (next === null) return
    event.preventDefault()
    radios[next]?.focus()
    onChange(options[next].value)
  }

  return (
    <div
      className={clsx('chips', layout !== 'box' && `chips--${layout}`, className)}
      role="radiogroup"
      aria-label={label}
      onKeyDown={onKeyDown}
    >
      {options.map((option, index) => {
        const on = option.value === value
        const chip = (
          <button
            key={option.value}
            type="button"
            role="radio"
            aria-checked={on}
            aria-label={option.ariaLabel}
            tabIndex={index === focusable ? 0 : -1}
            className={clsx('chip', on && 'chip--on')}
            onClick={() => onChange(option.value)}
          >
            {option.label}
          </button>
        )
        return option.hint ? (
          <Tooltip key={option.value} side="bottom" label={option.hint}>
            {chip}
          </Tooltip>
        ) : (
          chip
        )
      })}
    </div>
  )
}
