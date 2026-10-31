import { clsx } from 'clsx'
import type { ReactNode } from 'react'

export interface EmptyStateProps {
  icon?: ReactNode
  title: ReactNode
  body?: ReactNode
  /** The one thing to do next, if there is one. */
  action?: ReactNode
  /**
   * One quiet line in place of the centred block, for an empty list inside a
   * card, a dialog or a picker: "No passkeys enrolled yet." The page-sized
   * block is for a page or a card with nothing else in it. It draws no icon.
   */
  compact?: boolean
  className?: string
}

export function EmptyState({ icon, title, body, action, compact = false, className }: EmptyStateProps) {
  return (
    <div className={clsx('empty', compact && 'empty--compact', className)}>
      {icon && !compact ? (
        <span className="empty__icon" aria-hidden="true">
          {icon}
        </span>
      ) : null}
      <p className="empty__title">{title}</p>
      {body ? <p className="empty__body">{body}</p> : null}
      {action}
    </div>
  )
}
