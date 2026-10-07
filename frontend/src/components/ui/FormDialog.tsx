import { Fragment, type ReactNode } from 'react'

import { Dialog } from './Dialog'
import { useOpeningKey } from './opening-key'

/**
 * A dialog that holds a draft. Its children, a component that renders the
 * `DialogContent` and owns the draft's state, mount on the first opening and
 * fresh again on every later one, so a cancelled edit never shows up the next
 * time; they stay mounted while the dialog animates closed.
 */
export function FormDialog({
  open,
  onOpenChange,
  trigger,
  children,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** A `DialogTrigger`, which is there before the first opening. */
  trigger?: ReactNode
  children: ReactNode
}) {
  const opening = useOpeningKey(open)
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {trigger}
      {opening === 0 ? null : <Fragment key={opening}>{children}</Fragment>}
    </Dialog>
  )
}
