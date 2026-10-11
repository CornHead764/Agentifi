import { HoverCard as Radix } from 'radix-ui'
import { useRef, useState, type ComponentProps, type ReactNode } from 'react'

import { POPUP_INSET } from './popup-inset'

export interface HoverCardProps {
  content: ReactNode
  side?: ComponentProps<typeof Radix.Content>['side']
  align?: ComponentProps<typeof Radix.Content>['align']
  /** One element, which takes the pointer and focus handlers. */
  children: ReactNode
}

/**
 * A panel of detail that opens while the pointer rests on its trigger.
 *
 * Like a tooltip it never opens on touch, so what it says must be reachable
 * some other way too. Pressing the trigger closes it until the pointer
 * leaves, so it does not sit over the menu or dialog the press opened, nor
 * reopen when focus comes back to the trigger.
 */
export function HoverCard({ content, side = 'bottom', align = 'start', children }: HoverCardProps) {
  const [open, setOpen] = useState(false)
  const pressed = useRef(false)
  return (
    <Radix.Root
      open={open}
      onOpenChange={(next) => {
        if (!next || !pressed.current) setOpen(next)
      }}
      openDelay={350}
      closeDelay={120}
    >
      <Radix.Trigger
        asChild
        onPointerDown={() => {
          pressed.current = true
          setOpen(false)
        }}
        onPointerLeave={() => {
          pressed.current = false
        }}
      >
        {children}
      </Radix.Trigger>
      <Radix.Portal>
        <Radix.Content
          className="hovercard"
          side={side}
          align={align}
          sideOffset={6}
          collisionPadding={POPUP_INSET}
        >
          {content}
        </Radix.Content>
      </Radix.Portal>
    </Radix.Root>
  )
}
