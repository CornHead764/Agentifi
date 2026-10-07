import { clsx } from 'clsx'
import type { ComponentProps, ReactNode } from 'react'

import { RowActions } from './RowActions'

export interface ListProps extends ComponentProps<'ul'> {
  /** Rule a line between rows. Off for a short list inside a statement. */
  dividers?: boolean
}

/**
 * A column of `ListRow`s. The rows share one column template, so every row's
 * figures and actions line up with the rows above and below it.
 */
export function List({ dividers = true, className, ...props }: ListProps) {
  return <ul className={clsx('list', dividers && 'list--dividers', className)} {...props} />
}

export interface ListRowProps {
  title: ReactNode
  /** A mark after the title, on its line: pending, a role. */
  badge?: ReactNode
  /** The second line: an account, a date, what the row is waiting on. */
  sub?: ReactNode
  /** Right-aligned, in the numeric face. */
  figures?: ReactNode
  /** Under the figure: its date, its change. */
  figuresSub?: ReactNode
  /** `size="sm"` buttons or an `OverflowMenu`, always visible. */
  actions?: ReactNode
  /** Let the title wrap. For a row that is the whole of what it names: a question, a report. */
  wrap?: boolean
  /** Make the lead and figures one button. Actions stay outside it. */
  onSelect?: () => void
  /** Marks the row as the one on screen, with `aria-current`. */
  current?: boolean
  /** For a row whose `onSelect` shows or hides the rows under it: `aria-expanded`. */
  expanded?: boolean
  className?: string
}

/**
 * One row of a list that is not a table: a name, an optional second line, a
 * figure and the row's actions. One line is `--row-height` tall and two are
 * `--row-height-two-line`, the same as a table row at each.
 */
export function ListRow({
  title,
  badge,
  sub,
  figures,
  figuresSub,
  actions,
  wrap = false,
  onSelect,
  current,
  expanded,
  className,
}: ListRowProps) {
  const lead = (
    <>
      <span className="list-row__lead">
        <span className="list-row__title">
          {title}
          {badge ? <> {badge}</> : null}
        </span>
        {sub ? <span className="list-row__sub">{sub}</span> : null}
      </span>
      {figures || figuresSub ? (
        <span className="list-row__figures">
          {figures}
          {figuresSub ? <span className="list-row__sub">{figuresSub}</span> : null}
        </span>
      ) : null}
    </>
  )
  return (
    <li
      className={clsx(
        'list-row',
        (sub || figuresSub) && 'list-row--two-line',
        wrap && 'list-row--wrap',
        className,
      )}
    >
      {onSelect ? (
        <button
          type="button"
          className="list-row__press"
          aria-current={current ? 'true' : undefined}
          aria-expanded={expanded}
          onClick={onSelect}
        >
          {lead}
        </button>
      ) : (
        lead
      )}
      {actions ? (
        <span className="list-row__actions">
          <RowActions>{actions}</RowActions>
        </span>
      ) : null}
    </li>
  )
}
