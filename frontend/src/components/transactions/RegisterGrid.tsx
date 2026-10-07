import { useWindowVirtualizer } from '@tanstack/react-virtual'
import { ChevronDown, ChevronRight, CornerDownRight } from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type MouseEvent, type ReactNode } from 'react'

import { Money } from '@/components/Money'
import { Checkbox, SkeletonRows, SortButton } from '@/components/ui'
import { useMediaQuery } from '@/lib/useMediaQuery'
import {
  fitColumns,
  floorsRem,
  gridTemplate,
  NARROW_COLUMNS,
  NARROW_QUERY,
  narrowColumns,
  type ColumnDef,
  type ColumnId,
} from '@/lib/transactions/columns'
import {
  NARROW_ROW_TWO_LINE,
  ROW_HEIGHT_TOKENS,
  rowHeight,
  type RegisterRow,
  type RowHeight,
} from '@/lib/transactions/rows'
import { scaled, tokenPx } from '@/lib/scale'
import { selectAllState } from '@/lib/transactions/selection'
import { plural } from '@/lib/format'
import type { Transaction } from '@/lib/transactions/types'

import { PhoneRow } from './PhoneRow'
import { RegisterCell } from './RegisterCells'
import { RegisterContext, type RegisterView, type RowMenuControl } from './register-context'
import { displayPayee } from '@/lib/transactions/edits'

export interface RegisterGridProps {
  rows: readonly RegisterRow[]
  columns: readonly ColumnDef[]
  height: RowHeight
  view: RegisterView
  loading: boolean
  hasMore: boolean
  onLoadMore: () => void
  /**
   * The row's overflow menu. On a phone the row draws no button for it, so it
   * is handed a `control` and opened by a swipe instead — see RowMenuControl.
   */
  rowMenu?: (txn: Transaction, control?: RowMenuControl) => ReactNode
  empty: ReactNode
  /**
   * The order the query asked for. The grid never re-sorts: paging is the
   * server's, so a client flip would order only the loaded pages.
   */
  sort?: { order: 'asc' | 'desc'; onToggle: () => void }
  /**
   * A row a link pointed at. Scrolled into the middle of the viewport once
   * it is in `rows`, and marked for as long as it is on screen.
   */
  highlight?: string | null
  /**
   * Which requested columns are drawn. `fitColumns` drops what does not fit,
   * so a column the user switched on can be absent; Customize Columns says so.
   */
  onShownChange?: (shown: readonly ColumnId[]) => void
}

/* Everything that answers a click itself keeps it. Radix renders its triggers
   as buttons, so the element selector covers the pickers too. */
const ROW_CONTROLS = 'a, button, input, select, textarea, [role="button"], [role="checkbox"]'

/**
 * Open the row, unless the press was meant for something else.
 *
 * The press must have landed in the row's own markup: React bubbles a portal's
 * synthetic events up the component tree, so a click on a row-menu item or a
 * dialog it opened arrives here as a row click. And a drag-select across a
 * payee ends in a click, so a non-empty selection is left alone.
 */
function openRowDetail(event: MouseEvent<HTMLElement>, open: () => void) {
  const pressed = event.target
  if (!(pressed instanceof Element)) return
  if (pressed.closest('.register__row') === null) return
  if (pressed.closest(ROW_CONTROLS) !== null) return
  if (window.getSelection()?.isCollapsed === false) return
  open()
}

/**
 * The register, virtualized. Heights are computed rather than measured, so the
 * scrollbar is right on the first frame; the narrow layout's two-line rows are
 * placed at an estimate and then measured.
 */
export function RegisterGrid({
  rows,
  columns: requested,
  height,
  view,
  loading,
  hasMore,
  onLoadMore,
  rowMenu,
  empty,
  onShownChange,
  sort,
  highlight = null,
}: RegisterGridProps) {
  const viewport = useRef<HTMLDivElement>(null)
  const head = useRef<HTMLDivElement>(null)
  const phone = useMediaQuery(NARROW_QUERY)

  // The width the tracks get in rem, inside the head's padding, so the column
  // set can be cut to fit (see fitColumns). Zero until measured, which keeps
  // every requested column.
  const [widthRem, setWidthRem] = useState(0)
  // Where the list starts in the document, which the window virtualizer
  // places rows from. Content above it moves it without changing the list's
  // own box, so the page is watched as well.
  const [scrollMargin, setScrollMargin] = useState(0)
  useEffect(() => {
    const element = viewport.current
    if (!element || typeof ResizeObserver === 'undefined') return
    const measure = () => {
      const rootPx = Number.parseFloat(getComputedStyle(document.documentElement).fontSize) || 16
      const headStyle = head.current ? getComputedStyle(head.current) : null
      const inset = headStyle
        ? Number.parseFloat(headStyle.paddingLeft) + Number.parseFloat(headStyle.paddingRight)
        : 0
      setWidthRem((element.clientWidth - inset) / rootPx)
      setScrollMargin(element.getBoundingClientRect().top + window.scrollY)
    }
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(element)
    observer.observe(document.body)
    window.addEventListener('resize', measure)
    return () => {
      observer.disconnect()
      window.removeEventListener('resize', measure)
    }
  }, [])
  // `narrow` is a fact about this grid's width; the phone query is only the
  // answer before the first measurement.
  const extraRem = view.selection.enabled ? 4 : 2
  const { columns, narrow } = useMemo(() => {
    if (widthRem === 0 || phone) return { columns: requested, narrow: phone }
    const fitted = fitColumns(requested, widthRem, extraRem)
    if (floorsRem(fitted) + extraRem <= widthRem) return { columns: fitted, narrow: false }
    return { columns: narrowColumns(requested), narrow: true }
  }, [requested, widthRem, phone, extraRem])
  const minRowHeight = narrow ? scaled(NARROW_ROW_TWO_LINE) : 0
  // Read on every render: the root size, and so every rem, drops at the tablet
  // rung.
  const rowPx = tokenPx(ROW_HEIGHT_TOKENS[height])

  // A narrow register draws only `NARROW_COLUMNS`, in the two-line row.
  const shown = useMemo(
    () => (narrow ? NARROW_COLUMNS : columns.map((column) => column.id)),
    [columns, narrow],
  )
  useEffect(() => {
    onShownChange?.(shown)
  }, [onShownChange, shown])

  // The window, not a box of its own: a second scroller inside the page is
  // unreachable by a phone's status-bar tap. `scrollMargin` says where in the
  // document the rows begin.
  const virtualizer = useWindowVirtualizer({
    count: rows.length,
    estimateSize: (index) => rowHeight(rows[index], rowPx, minRowHeight),
    overscan: 12,
    scrollMargin,
  })

  useEffect(() => {
    // The virtualizer rebuilds its size cache only when the row count changes,
    // so a density change or rotation must ask for it.
    virtualizer.measure()
  }, [rowPx, minRowHeight, virtualizer])

  // Scroll to the highlighted row once per id: rows arrive a page at a time,
  // and scrolling on every refetch would fight the reader. `center` keeps it
  // clear of the sticky headers.
  const scrolledTo = useRef<string | null>(null)
  useEffect(() => {
    if (highlight === null || scrolledTo.current === highlight) return
    const index = rows.findIndex((row) => row.kind === 'transaction' && row.txn.id === highlight)
    if (index === -1) return
    scrolledTo.current = highlight
    virtualizer.scrollToIndex(index, { align: 'center' })
  }, [highlight, rows, virtualizer])

  const items = virtualizer.getVirtualItems()
  const last = items.at(-1)?.index ?? 0

  useEffect(() => {
    // Fetch the next page while there is still a screenful left to scroll
    // through, so the seam is never visible as a stall.
    if (hasMore && !loading && last >= rows.length - 25) onLoadMore()
  }, [hasMore, last, loading, onLoadMore, rows.length])

  // `visibleIds` is the page's list rather than a walk of `rows` here, which
  // would recompute on every scroll frame.
  const visibleCount = view.selection.visibleIds.length
  const allState = selectAllState(view.selection.visibleIds, view.selection.ids)

  const template = [view.selection.enabled ? '2rem' : null, gridTemplate(columns)]
    .filter((track) => track !== null)
    .join(' ')

  // A narrow row has no chrome: the checkbox track appears only in selection
  // mode, and a swipe opens the row menu.
  const narrowTemplate = [view.selection.active ? 'auto' : null, 'minmax(0, 1fr)']
    .filter((track) => track !== null)
    .join(' ')

  return (
    <RegisterContext value={view}>
      <div className={narrow ? 'register register--narrow' : 'register'}>
        {narrow && view.selection.enabled ? (
          // Narrow has no column head, the select-all's only other home.
          // Narrow is about this grid's width, not the device.
          <div className="register__selectall">
            <Checkbox
              checked={allState === 'all' ? true : allState === 'some' ? 'indeterminate' : false}
              onCheckedChange={view.selection.toggleAll}
              aria-label={
                allState === 'none'
                  ? `Select all ${plural(visibleCount, 'visible row')}`
                  : 'Clear the selection'
              }
            />
            <span className="register__selectall-label">
              {allState === 'none'
                ? `Select all ${plural(visibleCount, 'visible row')}`
                : 'Clear the selection'}
            </span>
          </div>
        ) : null}
        <div ref={head} className="register__head" style={{ gridTemplateColumns: template }}>
          {view.selection.enabled ? (
            <span className="register__th register__th--select">
                {/* Rows filtered out or in a closed section are not in `rows`,
                    so this box cannot reach them. */}
              <Checkbox
                checked={allState === 'all' ? true : allState === 'some' ? 'indeterminate' : false}
                onCheckedChange={view.selection.toggleAll}
                aria-label={
                  allState === 'none'
                    ? `Select all ${plural(visibleCount, 'visible row')}`
                    : 'Clear the selection'
                }
              />
            </span>
          ) : null}
          {columns.map((column) => (
            <span
              key={column.id}
              className={
                column.numeric
                  ? 'register__th register__th--numeric'
                  : column.iconOnly
                    ? 'register__th register__th--center'
                    : 'register__th'
              }
            >
              {column.iconOnly ? (
                <span className="visually-hidden">{column.label}</span>
              ) : sort && column.id === 'date' ? (
                <SortButton label={column.label} direction={sort.order} onSort={sort.onToggle}>
                  <span className="visually-hidden">
                    {sort.order === 'desc'
                      ? 'newest first; sort oldest first'
                      : 'oldest first; sort newest first'}
                  </span>
                </SortButton>
              ) : (
                column.label
              )}
            </span>
          ))}
          <span className="register__th" />
        </div>

        <div className="register__viewport" ref={viewport}>
          {rows.length === 0 && !loading ? (
            empty
          ) : (
            <div className="register__canvas" style={{ height: virtualizer.getTotalSize() }}>
              {items.map((item) => {
                const row = rows[item.index]
                  // `item.start` is a document offset; the canvas's own offset
                  // comes back off.
                const style = {
                  height: item.size,
                  transform: `translateY(${item.start - scrollMargin}px)`,
                }

                if (row.kind === 'group') {
                  return (
                    <div key={row.key} className="register__group" style={style}>
                      <button
                        type="button"
                        className="register__group-toggle"
                        aria-expanded={!row.collapsed}
                        onClick={() => view.sections.toggle(row.section)}
                      >
                        {row.collapsed ? (
                          <ChevronRight size={12} aria-hidden="true" />
                        ) : (
                          <ChevronDown size={12} aria-hidden="true" />
                        )}
                        <span>{row.label}</span>
                          {/* Only a closed section shows its count. */}
                        {row.collapsed ? (
                          <span className="register__group-count">
                            {plural(row.count, 'row')} hidden
                          </span>
                        ) : null}
                        <span className="register__group-total">
                          <Money value={row.total} tone="flow" showPlus />
                        </span>
                      </button>
                    </div>
                  )
                }

                if (row.kind === 'split') {
                  return (
                    <div key={row.key} className="split-row" style={style}>
                      <CornerDownRight size={12} aria-hidden="true" />
                      <span>{view.lookups.categoryName(row.split.category_id)}</span>
                      {row.split.memo ? <span>{row.split.memo}</span> : null}
                      <span className="split-row__amount">
                        <Money value={row.split.amount} tone="flow" showPlus />
                      </span>
                    </div>
                  )
                }

                if (narrow) {
                    // No `height`: `measureElement` sizes the row, and a height
                    // would cap the second line.
                  return (
                    <div
                      key={row.key}
                      className="register__row register__row--stacked"
                      data-index={item.index}
                      ref={virtualizer.measureElement}
                      style={{
                        transform: style.transform,
                        gridTemplateColumns: narrowTemplate,
                      }}
                      data-excluded={row.txn.excluded_from_reports}
                      data-highlight={row.txn.id === highlight || undefined}
                    >
                      {view.selection.active ? (
                        <span className="register__cell register__cell--select">
                          <Checkbox
                            checked={view.selection.ids.has(row.txn.id)}
                            onClick={(event) => view.selection.toggle(row.txn.id, event.shiftKey)}
                            aria-label={`Select ${displayPayee(row.txn)}`}
                          />
                        </span>
                      ) : null}
                      <PhoneRow
                        txn={row.txn}
                        menu={rowMenu && ((control) => rowMenu(row.txn, control))}
                      />
                    </div>
                  )
                }

                return (
                  <div
                    key={row.key}
                    className="register__row"
                    style={{ ...style, gridTemplateColumns: template }}
                    data-excluded={row.txn.excluded_from_reports}
                    data-highlight={row.txn.id === highlight || undefined}
                    // A convenience; the row menu is the keyboard path to the
                    // same dialog.
                    onClick={(event) => openRowDetail(event, () => view.actions.openDetail(row.txn))}
                  >
                    {view.selection.enabled ? (
                      <span className="register__cell register__cell--select">
                        <Checkbox
                          checked={view.selection.ids.has(row.txn.id)}
                          onClick={(event) => view.selection.toggle(row.txn.id, event.shiftKey)}
                          aria-label={`Select ${displayPayee(row.txn)}`}
                        />
                      </span>
                    ) : null}
                    {columns.map((column) => (
                      <span
                        key={column.id}
                        className={
                          column.numeric
                            ? 'register__cell register__cell--numeric'
                            : column.iconOnly
                              ? 'register__cell register__cell--center'
                              : 'register__cell'
                        }
                      >
                        <RegisterCell column={column} txn={row.txn} />
                      </span>
                    ))}
                    <span className="register__cell register__cell--center">
                      {rowMenu?.(row.txn)}
                    </span>
                  </div>
                )
              })}
            </div>
          )}
          {loading ? <SkeletonRows rows={6} className="register__loading" /> : null}
        </div>
      </div>
    </RegisterContext>
  )
}
