import { clsx } from 'clsx'
import { Slot } from 'radix-ui'
import type { ComponentProps } from 'react'

export type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger'
export type ButtonSize = 'sm' | 'md' | 'lg'

export interface ButtonProps extends ComponentProps<'button'> {
  variant?: ButtonVariant
  size?: ButtonSize
  /** Render the child element instead of a `<button>`, for links and Radix triggers. */
  asChild?: boolean
}

export function Button({
  variant = 'secondary',
  size = 'md',
  asChild = false,
  className,
  type,
  ...props
}: ButtonProps) {
  const Component = asChild ? Slot.Root : 'button'
  return (
    <Component
      // Buttons inside a form default to `submit`, which is how a filter panel
      // ends up saving a transaction.
      type={asChild ? undefined : (type ?? 'button')}
      className={clsx('btn', `btn--${variant}`, size !== 'md' && `btn--${size}`, className)}
      {...props}
    />
  )
}

export interface IconButtonProps extends Omit<ButtonProps, 'aria-label' | 'children' | 'size'> {
  /** Required: an icon-only control has no text for the accessibility tree to read. */
  label: string
  /** `sm` in a row, a chip or a toolbar of small controls; `md` everywhere else. */
  size?: 'sm' | 'md'
  children: ComponentProps<'button'>['children']
}

export function IconButton({ label, className, ...props }: IconButtonProps) {
  return <Button aria-label={label} className={clsx('btn--icon', className)} {...props} />
}
