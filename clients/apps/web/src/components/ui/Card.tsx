import { clsx } from 'clsx'
import type { ComponentProps, ReactNode } from 'react'

export interface CardProps extends Omit<ComponentProps<'section'>, 'title'> {
  title?: ReactNode
  subtitle?: ReactNode
  /** Controls that sit on the title row: a range picker, an overflow menu. */
  actions?: ReactNode
  /** Drop the body padding, for a card whose whole content is a table. */
  flush?: boolean
}

export function Card({
  title,
  subtitle,
  actions,
  flush = false,
  className,
  children,
  ...props
}: CardProps) {
  return (
    <section className={clsx('card', flush && 'card--flush', className)} {...props}>
      {title || actions ? (
        <header className="card__header">
          <div className="card__titles">
            {title ? <h3 className="card__title">{title}</h3> : null}
            {subtitle ? <p className="card__subtitle">{subtitle}</p> : null}
          </div>
          {actions ? <div className="card__actions">{actions}</div> : null}
        </header>
      ) : null}
      {flush ? children : <div className="card__body">{children}</div>}
    </section>
  )
}
