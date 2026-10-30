/**
 * "Are you sure?", in the app's own voice, rather than `window.confirm`: the
 * native dialog cannot be styled, is suppressed in some kiosk and installed-PWA
 * contexts (so "confirm then delete" becomes "nothing happens"), and blocks the
 * event loop.
 */

import type { ReactNode } from 'react'

import { Button } from './Button'
import { Dialog, DialogActions, DialogContent } from './Dialog'

export interface ConfirmDialogProps {
  open: boolean
  /** Asked as a question, and naming the thing: "Delete August groceries?" */
  title: ReactNode
  /** What the deletion actually does — the part a user cannot guess. */
  description?: ReactNode
  confirmLabel?: string
  cancelLabel?: string
  /** True while the write is in flight, so the button cannot be pressed twice. */
  pending?: boolean
  onConfirm: () => void
  onCancel: () => void
  /** Anything else worth saying: a count, a warning about what is kept. */
  children?: ReactNode
}

export function ConfirmDialog({
  open,
  title,
  description,
  confirmLabel = 'Delete',
  cancelLabel = 'Cancel',
  pending = false,
  onConfirm,
  onCancel,
  children,
}: ConfirmDialogProps) {
  return (
    <Dialog open={open} onOpenChange={(next) => (next ? undefined : onCancel())}>
      <DialogContent
        title={title}
        description={description}
        footer={
          <DialogActions cancel={cancelLabel} onCancel={onCancel} cancelDisabled={pending}>
            <Button variant="danger" onClick={onConfirm} disabled={pending}>
              {confirmLabel}
            </Button>
          </DialogActions>
        }
      >
        {children}
      </DialogContent>
    </Dialog>
  )
}
