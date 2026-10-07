import { clsx } from 'clsx'
import { Lock } from 'lucide-react'
import { useRef, useState, type ReactNode } from 'react'

import { titleWhenClipped } from './overflowTitle'

/**
 * A cell that becomes an input on a single click. Escape blurs the input, so
 * a naive `onBlur` commit would save the value being abandoned: the cancel
 * flag is set before the blur and read inside it.
 */
export interface TextCellProps {
  value: string
  /** Named for the accessibility tree: a grid of bare inputs is unreadable. */
  label: string
  placeholder?: string
  numeric?: boolean
  disabled?: boolean
  /** The idle display. Amount columns pass a `<Money>` so the figure is masked in privacy mode. */
  display?: ReactNode
  onCommit: (value: string) => void
}

export function TextCell({
  value,
  label,
  placeholder,
  numeric = false,
  disabled = false,
  display,
  onCommit,
}: TextCellProps) {
  const [editing, setEditing] = useState(false)
  const cancelled = useRef(false)

  if (!editing) {
    return (
      <button
        type="button"
        className={clsx('cell-edit', numeric && 'cell-edit--numeric', !value && 'cell-edit--empty')}
        // With a `display` node the button is named by its content, so a
        // masked amount is not read aloud through an `aria-label`.
        aria-label={display === undefined ? `${label}: ${value || 'empty'}` : undefined}
        disabled={disabled}
        // Not on a `display` cell: its text content is whatever the node
        // rendered, which in privacy mode is the mask.
        onMouseEnter={display === undefined ? titleWhenClipped : undefined}
        onClick={() => setEditing(true)}
      >
        {display === undefined ? (
          value || placeholder || '—'
        ) : (
          <>
            <span className="visually-hidden">{label} </span>
            {display}
          </>
        )}
      </button>
    )
  }

  return (
    <input
      autoFocus
      className={clsx('cell-input', numeric && 'cell-input--numeric')}
      aria-label={label}
      defaultValue={value}
      placeholder={placeholder}
      onKeyDown={(event) => {
        if (event.key === 'Enter') {
          cancelled.current = false
          event.currentTarget.blur()
        } else if (event.key === 'Escape') {
          cancelled.current = true
          event.currentTarget.blur()
        }
      }}
      onBlur={(event) => {
        const next = event.currentTarget.value
        const abandoned = cancelled.current
        cancelled.current = false
        setEditing(false)
        if (!abandoned && next !== value) onCommit(next)
      }}
    />
  )
}

export interface DateCellProps {
  value: string
  label: string
  display: string
  onCommit: (value: string) => void
}

export function DateCell({ value, label, display, onCommit }: DateCellProps) {
  const [editing, setEditing] = useState(false)
  const cancelled = useRef(false)

  if (!editing) {
    return (
      <button
        type="button"
        className="cell-edit"
        aria-label={`${label}: ${display}`}
        onMouseEnter={titleWhenClipped}
        onClick={() => setEditing(true)}
      >
        {display}
      </button>
    )
  }

  return (
    <input
      autoFocus
      type="date"
      className="cell-input"
      aria-label={label}
      defaultValue={value}
      onKeyDown={(event) => {
        if (event.key === 'Enter') {
          cancelled.current = false
          event.currentTarget.blur()
        } else if (event.key === 'Escape') {
          cancelled.current = true
          event.currentTarget.blur()
        }
      }}
      onBlur={(event) => {
        const next = event.currentTarget.value
        const abandoned = cancelled.current
        cancelled.current = false
        setEditing(false)
        if (!abandoned && next && next !== value) onCommit(next)
      }}
    />
  )
}

/**
 * A column that is visible and not editable. The statement name is what rules
 * and series match on, so it cannot be renamed; the cell says why.
 */
export function LockedCell({
  value,
  label,
  reason,
  onRefuse,
}: {
  value: string
  label: string
  reason: string
  onRefuse: (reason: string) => void
}) {
  return (
    <button
      type="button"
      className="cell-locked"
      title={value}
      aria-label={`${label}: ${value || 'empty'}. Read-only.`}
      onClick={() => onRefuse(reason)}
    >
      <span>{value || '—'}</span>
      <Lock className="cell-locked__lock" size={11} aria-hidden="true" />
    </button>
  )
}
