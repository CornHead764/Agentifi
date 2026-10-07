import { clsx } from 'clsx'
import { Check, ChevronDown, ChevronUp } from 'lucide-react'
import { Select as Radix } from 'radix-ui'
import { useRef, useState, type ReactNode } from 'react'

import { useFieldControl } from './field-context'
import { POPUP_INSET } from './popup-inset'

export interface SelectOption<V extends string = string> {
  value: V
  label: ReactNode
}

export interface OptionSelectProps<V extends string> {
  options: readonly SelectOption<V>[]
  /** `''` shows the placeholder. */
  value: V | ''
  onValueChange: (value: V) => void
  placeholder?: string
  /** `sm` in a table row or toolbar, beside `size="sm"` buttons. */
  size?: 'sm' | 'md'
  disabled?: boolean
  className?: string
  'aria-label'?: string
  /**
   * An item after the options that does something rather than being chosen,
   * such as revealing options the list leaves out. The list stays open.
   */
  action?: { label: string; onSelect: () => void }
}

/** The action item's value: no option's, since Radix refuses an empty one and ids are never this. */
const ACTION_VALUE = '\u0000action'

/**
 * One value picked from a list. The trigger carries the value, so it also
 * carries the field wiring: inside a `<Field>` it picks up the field's id and
 * error state without the caller repeating them.
 */
export function OptionSelect<V extends string>({
  options,
  value,
  onValueChange,
  placeholder,
  size = 'md',
  disabled,
  className,
  'aria-label': ariaLabel,
  action,
}: OptionSelectProps<V>) {
  const field = useFieldControl()
  const [open, setOpen] = useState(false)
  // Radix closes the list after any item is picked; picking the action is
  // the one pick that should leave it open.
  const acted = useRef(false)
  return (
    <Radix.Root
      value={value}
      open={open}
      onOpenChange={(next) => {
        if (!next && acted.current) {
          acted.current = false
          return
        }
        setOpen(next)
      }}
      onValueChange={(next) => {
        if (action && next === ACTION_VALUE) {
          acted.current = true
          action.onSelect()
          return
        }
        const chosen = options.find((option) => option.value === next)
        if (chosen) onValueChange(chosen.value)
      }}
      disabled={disabled}
    >
      <Radix.Trigger
        className={clsx('select__trigger', size === 'sm' && 'select__trigger--sm', className)}
        aria-label={ariaLabel}
        {...field}
      >
        <Radix.Value placeholder={placeholder} />
        <Radix.Icon className="select__icon">
          <ChevronDown size={14} />
        </Radix.Icon>
      </Radix.Trigger>
      <Radix.Portal>
        <Radix.Content
          className="select__content"
          position="popper"
          sideOffset={4}
          collisionPadding={POPUP_INSET}
        >
          <Radix.ScrollUpButton className="select__scroll">
            <ChevronUp size={14} />
          </Radix.ScrollUpButton>
          <Radix.Viewport className="select__viewport">
            {options.map((option) => (
              <Radix.Item key={option.value} value={option.value} className="menu__item">
                <span className="menu__item-indicator">
                  <Radix.ItemIndicator>
                    <Check size={14} />
                  </Radix.ItemIndicator>
                </span>
                <Radix.ItemText>{option.label}</Radix.ItemText>
              </Radix.Item>
            ))}
            {action ? (
              <Radix.Item value={ACTION_VALUE} className="menu__item select__action">
                <span className="menu__item-indicator" />
                <Radix.ItemText>{action.label}</Radix.ItemText>
              </Radix.Item>
            ) : null}
          </Radix.Viewport>
          <Radix.ScrollDownButton className="select__scroll">
            <ChevronDown size={14} />
          </Radix.ScrollDownButton>
        </Radix.Content>
      </Radix.Portal>
    </Radix.Root>
  )
}
