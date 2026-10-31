import { clsx } from 'clsx'
import { Check } from 'lucide-react'
import { DropdownMenu as Radix } from 'radix-ui'
import type { ComponentProps, ReactNode } from 'react'

import { POPUP_INSET } from './popup-inset'

export const DropdownMenu = Radix.Root
export const DropdownMenuTrigger = Radix.Trigger
export const DropdownMenuSub = Radix.Sub
export const DropdownMenuRadioGroup = Radix.RadioGroup

export function DropdownMenuContent({
  className,
  sideOffset = 6,
  align = 'end',
  collisionPadding = POPUP_INSET,
  ...props
}: ComponentProps<typeof Radix.Content>) {
  return (
    <Radix.Portal>
      <Radix.Content
        className={clsx('menu', className)}
        sideOffset={sideOffset}
        align={align}
        collisionPadding={collisionPadding}
        {...props}
      />
    </Radix.Portal>
  )
}

export interface DropdownMenuItemProps extends ComponentProps<typeof Radix.Item> {
  icon?: ReactNode
  danger?: boolean
}

export function DropdownMenuItem({
  icon,
  danger = false,
  className,
  children,
  ...props
}: DropdownMenuItemProps) {
  // `asChild` makes Radix clone the single child it is given. The indicator
  // slot would be a second one, and Slot answers that by throwing — which
  // unmounts the whole app rather than dropping the menu. An item rendered as
  // a link draws its own contents, so there is nothing to add.
  const contents = props.asChild ? (
    children
  ) : (
    <>
      {icon ? <span className="menu__item-indicator">{icon}</span> : null}
      {children}
    </>
  )
  return (
    <Radix.Item
      className={clsx('menu__item', danger && 'menu__item--danger', className)}
      {...props}
    >
      {contents}
    </Radix.Item>
  )
}

export function DropdownMenuCheckboxItem({
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

export function DropdownMenuRadioItem({
  className,
  children,
  ...props
}: ComponentProps<typeof Radix.RadioItem>) {
  return (
    <Radix.RadioItem className={clsx('menu__item', className)} {...props}>
      <span className="menu__item-indicator">
        <Radix.ItemIndicator>
          <Check size={14} />
        </Radix.ItemIndicator>
      </span>
      {children}
    </Radix.RadioItem>
  )
}

export function DropdownMenuLabel({ className, ...props }: ComponentProps<typeof Radix.Label>) {
  return <Radix.Label className={clsx('menu__label eyebrow', className)} {...props} />
}

export function DropdownMenuSeparator({
  className,
  ...props
}: ComponentProps<typeof Radix.Separator>) {
  return <Radix.Separator className={clsx('menu__separator', className)} {...props} />
}

export function DropdownMenuSubTrigger({
  className,
  ...props
}: ComponentProps<typeof Radix.SubTrigger>) {
  return <Radix.SubTrigger className={clsx('menu__item', className)} {...props} />
}

export function DropdownMenuSubContent({
  className,
  ...props
}: ComponentProps<typeof Radix.SubContent>) {
  return (
    <Radix.Portal>
      <Radix.SubContent className={clsx('menu', className)} {...props} />
    </Radix.Portal>
  )
}
