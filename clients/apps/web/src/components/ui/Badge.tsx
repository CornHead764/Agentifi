import { clsx } from 'clsx'
import type { ComponentProps } from 'react'

export type BadgeTone = 'neutral' | 'accent' | 'income' | 'expense' | 'warning'

export interface BadgeProps extends ComponentProps<'span'> {
  tone?: BadgeTone
  /** A count pill: the unread bell, "349 results". */
  count?: boolean
}

/**
 * `income` and `expense` carry the money colours, so they are for money
 * meanings only — received, overspent, past due. A status that is merely good
 * or bad news uses `neutral` or `accent`.
 */
export function Badge({ tone = 'neutral', count = false, className, ...props }: BadgeProps) {
  return (
    <span
      className={clsx('badge', tone !== 'neutral' && `badge--${tone}`, count && 'badge--count', className)}
      {...props}
    />
  )
}
