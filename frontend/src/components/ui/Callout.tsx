import { clsx } from 'clsx'
import { CircleAlert, Info, TriangleAlert } from 'lucide-react'
import type { ComponentProps, ReactNode } from 'react'

export type CalloutTone = 'info' | 'warning' | 'expense'

export interface CalloutProps extends Omit<ComponentProps<'div'>, 'title'> {
  /**
   * `info` for a fact about what is on screen, `warning` for something the
   * reader should act on or allow for, `expense` for a failure. `expense`
   * carries the money-out colour, so it is for a failure only.
   */
  tone?: CalloutTone
  /** The tone's own glyph unless given; `false` for none. */
  icon?: ReactNode
  /** A bold first line, for a callout with more than a sentence to say. */
  title?: ReactNode
  /** Buttons at the end, `size="sm"`. */
  actions?: ReactNode
}

const ICONS: Record<CalloutTone, ReactNode> = {
  info: <Info size={14} />,
  warning: <TriangleAlert size={14} />,
  expense: <CircleAlert size={14} />,
}

/**
 * A notice inside a page, card or dialog: quiet, in the tone's colour, never
 * a card of its own. A toast is for what just happened; this is for what is
 * true while it is on screen.
 */
export function Callout({
  tone = 'info',
  icon,
  title,
  actions,
  className,
  children,
  ...props
}: CalloutProps) {
  const glyph = icon === undefined ? ICONS[tone] : icon
  return (
    <div className={clsx('callout', `callout--${tone}`, className)} {...props}>
      {glyph ? (
        <span className="callout__icon" aria-hidden="true">
          {glyph}
        </span>
      ) : null}
      <div className="callout__body">
        {title ? <p className="callout__title">{title}</p> : null}
        {children}
      </div>
      {actions ? <div className="callout__actions">{actions}</div> : null}
    </div>
  )
}
