import { clsx } from 'clsx'
import type { ReactNode } from 'react'

export interface PageHeaderProps {
  /** Before the title: a back button. */
  leading?: ReactNode
  title?: ReactNode
  subtitle?: ReactNode
  /** A `TabsList`. The header then draws the strip's rule under the whole row. */
  tabs?: ReactNode
  /** Controls that follow the title or tabs: a range picker, a badge, a note. */
  children?: ReactNode
  /** Buttons at the right end, `size="sm"`. */
  actions?: ReactNode
  className?: string
}

/**
 * The first row of a page, or of a flush card: what the page is showing on the
 * left, what can be done on the right.
 */
export function PageHeader({
  leading,
  title,
  subtitle,
  tabs,
  children,
  actions,
  className,
}: PageHeaderProps) {
  return (
    <header className={clsx('page-header', tabs ? 'page-header--tabs' : null, className)}>
      {leading}
      {title ? (
        <div className="page-header__titles">
          <h2 className="page-header__title">{title}</h2>
          {subtitle ? <p className="page-header__subtitle">{subtitle}</p> : null}
        </div>
      ) : null}
      {tabs}
      {children}
      {actions ? <div className="page-header__actions">{actions}</div> : null}
    </header>
  )
}
