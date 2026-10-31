import { clsx } from 'clsx'
import { X } from 'lucide-react'
import { Dialog as RadixDialog } from 'radix-ui'
import type { ComponentProps, ReactNode } from 'react'

import { IconButton } from './Button'

export const Sheet = RadixDialog.Root
export const SheetTrigger = RadixDialog.Trigger

export interface SheetContentProps
  extends Omit<ComponentProps<typeof RadixDialog.Content>, 'title'> {
  title: ReactNode
}

/**
 * A panel up from the bottom edge, for a phone: a modal dialog, so the page
 * under it cannot scroll it away. The caller's children scroll inside it.
 */
export function SheetContent({ title, className, children, ...props }: SheetContentProps) {
  return (
    <RadixDialog.Portal>
      <RadixDialog.Overlay className="overlay" />
      <RadixDialog.Content
        className={clsx('sheet', className)}
        aria-describedby={undefined}
        {...props}
      >
        <div className="sheet__header">
          <RadixDialog.Title className="sheet__title">{title}</RadixDialog.Title>
          <RadixDialog.Close asChild>
            <IconButton variant="ghost" label="Close">
              <X size={18} />
            </IconButton>
          </RadixDialog.Close>
        </div>
        {children}
      </RadixDialog.Content>
    </RadixDialog.Portal>
  )
}
