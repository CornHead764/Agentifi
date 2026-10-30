import { clsx } from 'clsx'
import { useId, useMemo, type ReactNode } from 'react'

import { FieldContext } from './field-context'

export interface FieldProps {
  label: ReactNode
  hint?: ReactNode
  error?: ReactNode
  /** Rendered as a legend-less group instead of a label, for radio sets. */
  as?: 'label' | 'group'
  className?: string
  children: ReactNode
}

export function Field({ label, hint, error, as = 'label', className, children }: FieldProps) {
  const base = useId()
  const controlId = `${base}-control`
  const hintId = `${base}-hint`
  const errorId = `${base}-error`

  const control = useMemo(() => {
    const described = [hint ? hintId : null, error ? errorId : null].filter(Boolean).join(' ')
    return {
      // A group must not hand its id to its controls: they would share a DOM
      // id, and a label pointing at a duplicated id activates the first
      // element in the document. The group's own label is a span, not a
      // <label for>.
      id: as === 'group' ? undefined : controlId,
      'aria-describedby': described || undefined,
      'aria-invalid': error ? true : undefined,
    }
  }, [as, controlId, error, errorId, hint, hintId])

  const labelNode =
    as === 'label' ? (
      <label className="field__label" htmlFor={controlId}>
        {label}
      </label>
    ) : (
      <span className="field__label">{label}</span>
    )

  const body = (
    <>
      {labelNode}
      <FieldContext value={control}>{children}</FieldContext>
      {hint ? (
        <span className="field__hint" id={hintId}>
          {hint}
        </span>
      ) : null}
      {error ? (
        <span className="field__error" id={errorId} role="alert">
          {error}
        </span>
      ) : null}
    </>
  )

  if (as === 'group') {
    return (
      <div className={clsx('field', className)} role="group" aria-describedby={control['aria-describedby']}>
        {body}
      </div>
    )
  }

  return <div className={clsx('field', className)}>{body}</div>
}
