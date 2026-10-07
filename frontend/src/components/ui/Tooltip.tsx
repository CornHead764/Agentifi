import { Tooltip as Radix } from 'radix-ui'
import type { ComponentProps, ReactNode } from 'react'

export const TooltipProvider = Radix.Provider

export interface TooltipProps {
  label: ReactNode
  side?: ComponentProps<typeof Radix.Content>['side']
  children: ReactNode
}

/**
 * A tooltip is a hint, never the only label.
 *
 * Radix tooltips do not open on touch or from a screen reader, so every
 * icon-only control still carries its own `aria-label` — `IconButton` requires
 * one — and this repeats it for the pointer.
 */
export function Tooltip({ label, side = 'right', children }: TooltipProps) {
  return (
    <Radix.Root>
      <Radix.Trigger asChild>{children}</Radix.Trigger>
      <Radix.Portal>
        <Radix.Content className="tooltip" side={side} sideOffset={6}>
          {label}
          <Radix.Arrow className="tooltip__arrow" width={10} height={5} />
        </Radix.Content>
      </Radix.Portal>
    </Radix.Root>
  )
}
