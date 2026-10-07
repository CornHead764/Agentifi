import { clsx } from 'clsx'
import { RadioGroup as Radix } from 'radix-ui'
import { useId, type ComponentProps, type ReactNode } from 'react'

import { ControlRow } from './control-row'

export function RadioGroup({ className, ...props }: ComponentProps<typeof Radix.Root>) {
  return <Radix.Root className={clsx('radio-group', className)} {...props} />
}

export interface RadioProps extends ComponentProps<typeof Radix.Item> {
  label: ReactNode
}

export function Radio({ label, className, id, ...props }: RadioProps) {
  const generated = useId()
  const controlId = id ?? generated
  return (
    <ControlRow
      className={className}
      controlId={controlId}
      label={label}
      control={
        <Radix.Item className="radio" id={controlId} {...props}>
          <Radix.Indicator className="radio__dot" />
        </Radix.Item>
      }
    />
  )
}
