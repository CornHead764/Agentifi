import { clsx } from 'clsx'
import { Switch as Radix } from 'radix-ui'
import { useId, type ComponentProps, type ReactNode } from 'react'

import { ControlRow } from './control-row'
import { useFieldControl } from './field-context'

export interface SwitchProps extends ComponentProps<typeof Radix.Root> {
  label?: ReactNode
  /** Put the label before the track, as the settings screens do. */
  labelPosition?: 'before' | 'after'
}

export function Switch({
  label,
  labelPosition = 'after',
  className,
  id,
  ...props
}: SwitchProps) {
  const field = useFieldControl()
  const generated = useId()
  const controlId = id ?? field.id ?? generated

  const track = (
    <Radix.Root className={clsx('switch', !label && className)} id={controlId} {...props}>
      <Radix.Thumb className="switch__thumb" />
    </Radix.Root>
  )

  if (!label) return track

  return (
    <ControlRow
      className={className}
      controlId={controlId}
      control={track}
      label={label}
      labelPosition={labelPosition}
    />
  )
}
