import { clsx } from 'clsx'
import { Check } from 'lucide-react'
import { ContextMenu as Radix } from 'radix-ui'
import type { ComponentProps, ReactNode } from 'react'

/**
 * A menu that opens where the pointer is.
 *
 * Radix gives right-click, a 700ms long-press on touch and pen, and the
 * platform's context-menu key — the last one only fires on whatever inside the
 * trigger has focus, so a region that offers this menu owes the keyboard a
 * focusable element too. `openContextMenuAt` in ./context-menu-open is that
 * element's half of the deal.
 *
 * The classes are `.menu`'s, the ones DropdownMenu draws: one menu appearance,
 * two ways of summoning it.
 */
export const ContextMenu = Radix.Root
export const ContextMenuTrigger = Radix.Trigger

export function ContextMenuContent({ className, ...props }: ComponentProps<typeof Radix.Content>) {
  return (
    <Radix.Portal>
      <Radix.Content className={clsx('menu', className)} {...props} />
    </Radix.Portal>
  )
}

export interface ContextMenuItemProps extends ComponentProps<typeof Radix.Item> {
  icon?: ReactNode
  danger?: boolean
}

export function ContextMenuItem({
  icon,
  danger = false,
  className,
  children,
  ...props
}: ContextMenuItemProps) {
  return (
    <Radix.Item
      className={clsx('menu__item', danger && 'menu__item--danger', className)}
      {...props}
    >
      {icon ? <span className="menu__item-indicator">{icon}</span> : null}
      {children}
    </Radix.Item>
  )
}

export function ContextMenuCheckboxItem({
  className,
  children,
  ...props
}: ComponentProps<typeof Radix.CheckboxItem>) {
  return (
    <Radix.CheckboxItem className={clsx('menu__item', className)} {...props}>
      <span className="menu__item-indicator">
        <Radix.ItemIndicator>
          <Check size={14} />
        </Radix.ItemIndicator>
      </span>
      {children}
    </Radix.CheckboxItem>
  )
}

export function ContextMenuLabel({ className, ...props }: ComponentProps<typeof Radix.Label>) {
  return <Radix.Label className={clsx('menu__label eyebrow', className)} {...props} />
}

export function ContextMenuSeparator({
  className,
  ...props
}: ComponentProps<typeof Radix.Separator>) {
  return <Radix.Separator className={clsx('menu__separator', className)} {...props} />
}
