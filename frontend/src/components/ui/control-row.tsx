import { clsx } from 'clsx'
import type { MouseEvent, ReactNode } from 'react'

/** The box or track beside its label, for `Radio`, `Checkbox` and `Switch` alike. */
export function ControlRow({
  control,
  label,
  controlId,
  labelPosition = 'after',
  className,
  onLabelMouseDown,
}: {
  control: ReactNode
  label: ReactNode
  controlId: string
  labelPosition?: 'before' | 'after'
  className?: string
  onLabelMouseDown?: (event: MouseEvent) => void
}) {
  const text = (
    <label className="control-row__label" htmlFor={controlId} onMouseDown={onLabelMouseDown}>
      {label}
    </label>
  )
  return (
    <span className={clsx('row control-row', className)}>
      {labelPosition === 'before' ? text : null}
      {control}
      {labelPosition === 'after' ? text : null}
    </span>
  )
}
