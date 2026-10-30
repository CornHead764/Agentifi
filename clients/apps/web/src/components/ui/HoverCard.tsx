import { HoverCard as Radix } from 'radix-ui'
import { useEffect, useRef, useState, type ComponentProps, type ReactNode } from 'react'

import { POPUP_INSET } from './popup-inset'

export interface HoverCardProps {
  content: ReactNode
  side?: ComponentProps<typeof Radix.Content>['side']
  align?: ComponentProps<typeof Radix.Content>['align']
  /** Kept shut, as while a menu the trigger opened is up. */
  disabled?: boolean
  /** One element, which takes the pointer and focus handlers. */
  children: ReactNode
}

/**
 * A panel of detail that opens while the pointer rests on its trigger.
 *
 * Only the pointer opens it, never focus: it would otherwise come up when a
 * menu the trigger opened closes and hands focus back. Like a tooltip it never
 * opens on touch, so what it says must be reachable some other way too.
 * Pressing the trigger closes it until the pointer leaves, so it does not sit
 * over the menu or dialog the press opened; so does coming out of disabled.
 */
export function HoverCard({
  content,
  side = 'bottom',
  align = 'start',
  disabled = false,
  children,
}: HoverCardProps) {
  const [open, setOpen] = useState(false)
  const pressed = useRef(false)
  const inside = useRef(false)
  // Coming out of disabled counts as a press: the menu closing is not a hover.
  const wasDisabled = useRef(disabled)
  useEffect(() => {
    if (wasDisabled.current && !disabled) pressed.current = true
    wasDisabled.current = disabled
  }, [disabled])
  return (
    <Radix.Root
      open={open && !disabled}
      onOpenChange={(next) => {
        if (next && (pressed.current || !inside.current || disabled)) return
        setOpen(next)
      }}
      openDelay={350}
      closeDelay={120}
    >
      <Radix.Trigger
        asChild
        onPointerEnter={() => {
          inside.current = true
        }}
        onPointerDown={() => {
          pressed.current = true
          setOpen(false)
        }}
        onPointerLeave={() => {
          inside.current = false
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
