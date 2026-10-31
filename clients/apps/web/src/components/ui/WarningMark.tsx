import { clsx } from 'clsx'
import { TriangleAlert } from 'lucide-react'

import { Tooltip, type TooltipProps } from './Tooltip'

export interface WarningMarkProps {
  /** What is wrong, as a sentence worded from the data: the tooltip and the accessible name. */
  text: string
  /** A word drawn beside the triangle, such as "Incomplete". */
  label?: string
  /** Drawn as a warning `Badge`. */
  badge?: boolean
  side?: TooltipProps['side']
  /** False inside a link or a button, which is already a tab stop and takes the text into its own name. */
  focusable?: boolean
  className?: string
}

/**
 * The amber triangle. It never stands alone: the tooltip says what it warns
 * about, and the same sentence is its accessible name. Focusable, so a
 * keyboard opens the tooltip too.
 */
export function WarningMark({
  text,
  label,
  badge = false,
  side = 'top',
  focusable = true,
  className,
}: WarningMarkProps) {
  return (
    <Tooltip label={text} side={side}>
      <span
        className={clsx('warning-mark', badge && 'badge badge--warning', className)}
        role="img"
        aria-label={label ? `${label}: ${text}` : text}
        tabIndex={focusable ? 0 : undefined}
      >
        <TriangleAlert size={badge ? 12 : 13} aria-hidden="true" />
        {label}
      </span>
    </Tooltip>
  )
}
