import { clsx } from 'clsx'
import { ArrowDown, ArrowUp } from 'lucide-react'
import { useState, useRef, type ComponentProps, type ReactNode } from 'react'

import { useBrowserLayoutEffect, useOverflowEdges } from './overflow-edges'

/**
 * The width each table stacks at, two rungs of the breakpoint ladder
 * (tokens.css). Per table: five columns of switches still read on a portrait
 * tablet; six columns of transfers do not.
 */
const STACK_QUERY = {
  phone: '(max-width: 30rem)',
  tablet: '(max-width: 48rem)',
} as const

export interface TableProps extends ComponentProps<'table'> {
  /** Row height. The register offers all three under Customize Columns. */
  density?: 'sm' | 'md' | 'lg'
  /**
   * `2` when any row has a second line (`.cell__sub`): every row then takes
   * the two-line height, whether or not its own cells wrap, and `density` is
   * not read.
   */
  lines?: 1 | 2
    /**
     * Redraw each row as a labelled block, on a phone (`true`) or from a tablet
     * down (`'tablet'`). Opt-in: a stacked row needs a design of its own.
     */
  stack?: boolean | 'tablet'
}

/** Whether this table stacks now. Read during render, so a grid does not paint and then reflow. */
function useStacked(stack: boolean | 'tablet'): boolean {
  const query = stack === 'tablet' ? STACK_QUERY.tablet : STACK_QUERY.phone
  const media = () =>
    Boolean(stack) && typeof window !== 'undefined' && typeof window.matchMedia === 'function'
      ? window.matchMedia(query).matches
      : false

  const [stacked, setStacked] = useState(media)

  useBrowserLayoutEffect(() => {
    if (!stack || typeof window === 'undefined' || typeof window.matchMedia !== 'function') return
    const watch = window.matchMedia(query)
    const apply = () => setStacked(watch.matches)
    apply()
    watch.addEventListener('change', apply)
    return () => watch.removeEventListener('change', apply)
  }, [stack, query])

  return stacked
}

/**
 * A dense grid with a sticky header. The scroll container is part of the
 * component because `position: sticky` resolves against the nearest scrolling
 * ancestor. The frame's edge shadow is driven by `data-overflow-*`, measured
 * here.
 */
export function Table({
  density = 'md',
  lines = 1,
  stack = false,
  className,
  children,
  ...props
}: TableProps) {
  const scroller = useRef<HTMLDivElement>(null)
  const table = useRef<HTMLTableElement>(null)
  const stacked = useStacked(stack)

  useOverflowEdges(scroller, table)

  // Stacked cells name their own column, and a caller that has not said so on
  // every `Td` gets the heading copied down. Runs on every render because a
  // heading can change; `data-label-fixed` marks the ones the caller chose.
  useBrowserLayoutEffect(() => {
    const element = table.current
    if (!element || !stack) return
    const headings = Array.from(element.querySelectorAll(':scope > thead th')).map(
      (cell) => cell.textContent?.trim() ?? '',
    )
    for (const row of element.querySelectorAll(':scope > tbody > tr')) {
      let column = 0
      for (const cell of row.children) {
        if (!(cell instanceof HTMLTableCellElement)) continue
        if (cell.dataset.labelFixed === undefined) {
          const heading = headings[column] ?? ''
          if (heading) cell.dataset.label = heading
          else delete cell.dataset.label
        }
        column += cell.colSpan
      }
    }
  })

  return (
    <div className={clsx('table-frame', stacked && 'table-frame--stacked')}>
      <div className="table-scroll" ref={scroller}>
        <table
          ref={table}
          className={clsx(
            'table',
            lines === 2 ? 'table--two-line' : density !== 'md' && `table--${density}`,
            // The opt-in, which is true at every width, and the state, which
            // is what the stylesheet acts on.
            stack && 'table--stack',
            stacked && 'table--stacked',
            className,
          )}
          {...props}
        >
          {children}
        </table>
      </div>
    </div>
  )
}

export interface CellProps extends ComponentProps<'td'> {
  /** Right-aligned tabular digits. Every column of amounts sets this. */
  numeric?: boolean
  /**
   * The column name a stacked row prints beside this cell. `''` means the
   * cell leads its row and needs none; omitted means copy the heading down.
   */
  label?: string
}

export function Td({ numeric = false, label, className, ...props }: CellProps) {
  return (
    <td
      className={clsx(numeric && 'numeric', className)}
      data-label={label}
      data-label-fixed={label === undefined ? undefined : ''}
      {...props}
    />
  )
}

export function Th({
  numeric = false,
  className,
  ...props
}: ComponentProps<'th'> & { numeric?: boolean }) {
  return <th className={clsx(numeric && 'numeric', className)} {...props} />
}

export type SortDirection = 'asc' | 'desc'

/**
 * A column head's sort control: its label, with an arrow only while the column
 * is the sort. `children` is read after the arrow, for a head that is not a
 * table cell and so cannot say its order through `aria-sort`.
 */
export function SortButton({
  label,
  direction,
  onSort,
  children,
}: {
  label: ReactNode
  direction: SortDirection | null
  onSort: () => void
  children?: ReactNode
}) {
  return (
    <button type="button" className="table__sort" onClick={onSort}>
      {label}
      {direction === 'asc' ? <ArrowUp size={12} aria-hidden="true" /> : null}
      {direction === 'desc' ? <ArrowDown size={12} aria-hidden="true" /> : null}
      {children}
    </button>
  )
}

/** A table column head that sorts; `aria-sort` carries the arrow to a screen reader. */
export function SortableTh({
  label,
  direction,
  onSort,
  numeric = false,
}: {
  label: ReactNode
  direction: SortDirection | null
  onSort: () => void
  numeric?: boolean
}) {
  return (
    <Th
      numeric={numeric}
      aria-sort={
        direction === 'asc' ? 'ascending' : direction === 'desc' ? 'descending' : undefined
      }
    >
      <SortButton label={label} direction={direction} onSort={onSort} />
    </Th>
  )
}

/** A subhead spanning the grid: "Pending", then one per month. */
export function TableGroupRow({ colSpan, children }: { colSpan: number; children: ReactNode }) {
  return (
    <tr className="table__group">
      <td colSpan={colSpan}>{children}</td>
    </tr>
  )
}

export function TableEmptyRow({ colSpan, children }: { colSpan: number; children: ReactNode }) {
  return (
    <tr className="table__empty">
      <td colSpan={colSpan}>{children}</td>
    </tr>
  )
}
