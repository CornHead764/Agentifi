import { clsx } from 'clsx'
import { Search, X } from 'lucide-react'
import type { ComponentProps, ReactNode } from 'react'

import { focusFieldOnPress } from './field-box'
import { useFieldControl } from './field-context'

export interface SearchInputProps
  extends Omit<ComponentProps<'input'>, 'value' | 'onChange' | 'className' | 'children' | 'size'> {
  value: string
  onChange: (next: string) => void
  /** The box's class, for how it sits in its row. */
  className?: string
  /** `sm` in a toolbar or a card's or header's actions, beside `size="sm"` buttons. */
  size?: 'sm' | 'md'
  /** Controls inside the box after the field, such as a Shortcuts popover. */
  children?: ReactNode
}

/**
 * A magnifier, the field and a clear button as one box. Fully controlled: a
 * mirrored local state renders the old text for a frame after the owner
 * clears it, so any debounce lives with the owner. Escape clears the text.
 */
export function SearchInput({
  value,
  onChange,
  className,
  size = 'md',
  children,
  onKeyDown,
  ...props
}: SearchInputProps) {
  const field = useFieldControl()
  return (
    <div
      className={clsx('search', size === 'sm' && 'search--sm', className)}
      onMouseDown={focusFieldOnPress}
    >
      <Search size={14} aria-hidden="true" />
      <input
        className="search__input"
        autoComplete="off"
        {...field}
        {...props}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Escape' && value !== '') onChange('')
          onKeyDown?.(event)
        }}
      />
      {value ? (
        <button
          type="button"
          className="search__clear"
          aria-label="Clear the search"
          onClick={() => onChange('')}
        >
          <X size={13} aria-hidden="true" />
        </button>
      ) : null}
      {children}
    </div>
  )
}
