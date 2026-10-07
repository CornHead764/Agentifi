import { clsx } from 'clsx'
import { Check, Minus } from 'lucide-react'
import { Checkbox as Radix } from 'radix-ui'
import { useId, type ComponentProps, type MouseEvent, type ReactNode } from 'react'

import { ControlRow } from './control-row'
import { useFieldControl } from './field-context'

export interface CheckboxProps extends ComponentProps<typeof Radix.Root> {
  /** Rendered beside the box and wired to it; omit only inside a labelled cell. */
  label?: ReactNode
}

/** A shift-click extends a list's selection, so it must not also select page text. */
function keepTextSelection(event: MouseEvent) {
  if (event.shiftKey) event.preventDefault()
}

export function Checkbox({ label, className, id, onMouseDown, ...props }: CheckboxProps) {
  const field = useFieldControl()
  const generated = useId()
  const controlId = id ?? field.id ?? generated

  const box = (
    <Radix.Root
      className={clsx('checkbox', !label && className)}
      id={controlId}
      onMouseDown={(event) => {
        keepTextSelection(event)
        onMouseDown?.(event)
      }}
      {...props}
    >
      <Radix.Indicator>
        {props.checked === 'indeterminate' ? <Minus size={12} /> : <Check size={12} />}
      </Radix.Indicator>
    </Radix.Root>
  )

  if (!label) return box

  return (
    <ControlRow
      className={className}
      controlId={controlId}
      control={box}
      label={label}
      onLabelMouseDown={keepTextSelection}
    />
  )
}
