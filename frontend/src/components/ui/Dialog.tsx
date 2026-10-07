import { clsx } from 'clsx'
import { X } from 'lucide-react'
import { Dialog as RadixDialog } from 'radix-ui'
import type { ComponentProps, FormEvent, ReactNode } from 'react'

import { Button, IconButton } from './Button'
import { submitOwnForm } from './dialog-submit'

export const Dialog = RadixDialog.Root
export const DialogTrigger = RadixDialog.Trigger
export const DialogClose = RadixDialog.Close

export interface DialogContentProps
  extends Omit<ComponentProps<typeof RadixDialog.Content>, 'title' | 'onSubmit'> {
  /** Required by Radix for the accessible name, and by the screens, which all have one. */
  title: ReactNode
  description?: ReactNode
  footer?: ReactNode
  wide?: boolean
  /**
   * For a dialog whose body is a list somebody filters. Its height becomes its
   * own rather than its content's, so narrowing the list does not shrink the
   * box and move the field being typed into. Nearly full screen on a phone.
   */
  fills?: boolean
  /**
   * Wraps the body and footer in a `<form>` so a submit button in the footer
   * (or Enter in a field) fires this instead of needing a click handler.
   */
  onSubmit?: (event: FormEvent<HTMLFormElement>) => void
}

export function DialogContent({
  title,
  description,
  footer,
  wide = false,
  fills = false,
  className,
  children,
  onSubmit,
  ...props
}: DialogContentProps) {
  // Radix warns about a dialog with no description. Passing the key explicitly
  // as undefined is how it is told "this one has none" rather than "forgotten".
  const describedBy = description ? {} : { 'aria-describedby': undefined }

  return (
    <RadixDialog.Portal>
      <RadixDialog.Overlay className="overlay" />
      <RadixDialog.Content
        className={clsx('dialog', wide && 'dialog--wide', fills && 'dialog--fills', className)}
        {...describedBy}
        {...props}
      >
        <div className="dialog__header">
          <RadixDialog.Title className="dialog__title">{title}</RadixDialog.Title>
          <RadixDialog.Close asChild>
            <IconButton label="Close" variant="ghost" size="sm">
              <X size={16} />
            </IconButton>
          </RadixDialog.Close>
        </div>
        {description ? (
          <RadixDialog.Description className="dialog__description">
            {description}
          </RadixDialog.Description>
        ) : null}
        {onSubmit ? (
          // `display: contents` keeps body and footer as direct flex children of
          // `.dialog`, so the form adds Enter-to-submit without changing layout.
          <form
            className="dialog__form"
            onSubmit={(event) => submitOwnForm(event, onSubmit)}
          >
            <div className="dialog__body">{children}</div>
            {footer ? <div className="dialog__footer">{footer}</div> : null}
          </form>
        ) : (
          <>
            <div className="dialog__body">{children}</div>
            {footer ? <div className="dialog__footer">{footer}</div> : null}
          </>
        )}
      </RadixDialog.Content>
    </RadixDialog.Portal>
  )
}

export interface DialogActionsProps {
  /**
   * The dismissing button's word: "Cancel" where there is a draft to throw
   * away, "Close" where there is none. `false` for a dialog whose answer is
   * also its only way out ("Done").
   */
  cancel?: string | false
  /** Called in place of closing the dialog, for a caller that must know. */
  onCancel?: () => void
  /** True while the write is in flight, so the dialog is not left mid-write. */
  cancelDisabled?: boolean
  /**
   * What is not an answer (a delete, a reset, a preview, a link) goes first,
   * pushed to the far edge from the answer.
   */
  start?: ReactNode
  /** The answer: one `primary` or `danger` button, which comes last. */
  children?: ReactNode
}

/**
 * The footer's buttons, in the one order every dialog uses: anything that is
 * not an answer on the left, then Cancel as a secondary button, then the
 * answer. Goes in `DialogContent`'s `footer`.
 */
export function DialogActions({
  cancel = 'Cancel',
  onCancel,
  cancelDisabled = false,
  start,
  children,
}: DialogActionsProps) {
  const dismiss =
    cancel === false ? null : onCancel ? (
      <Button onClick={onCancel} disabled={cancelDisabled}>
        {cancel}
      </Button>
    ) : (
      <RadixDialog.Close asChild>
        <Button disabled={cancelDisabled}>{cancel}</Button>
      </RadixDialog.Close>
    )
  return (
    <>
      {start}
      {start ? <span className="dialog__footer-gap" /> : null}
      {dismiss}
      {children}
    </>
  )
}
