import { BookmarkPlus, ChevronDown, HandCoins, Settings2, Sparkles, Split, Zap } from 'lucide-react'
import { Fragment, type ReactNode } from 'react'

import { Money } from '@/components/Money'
import {
  Button,
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui'
import { formatCount, formatDate, plural } from '@/lib/format'
import type { RegisterQuery } from '@/lib/transactions/api'
import type { QuickFilter, QuickFilterGroup } from '@/lib/transactions/quickFilters'
import type { AccountWindowSummary, TransactionPage } from '@/lib/transactions/types'

const GROUP_LABELS: Record<QuickFilterGroup, string | null> = {
  general: null,
  review: 'Review',
  saved: 'Saved',
}

export interface QuickFiltersProps {
  /** The Filter popover, first in the row. */
  filter?: ReactNode
  /** What the chart's drill narrowed to, as removable chips beside the popover. */
  narrowing?: ReactNode
  /** The date window, after the one-tap filters. It sets every card on the page, not only the register. */
  dates?: ReactNode
  /** The search box, at the row's right end. */
  search?: ReactNode
  /** The register's first page, whose count, total and window describe the whole query. */
  summary: TransactionPage | null
  /** The scoped account's balances into and out of the window, when one account is shown. */
  carried: AccountWindowSummary | null
  /** The built-in quick filters, then the saved ones, in menu order. */
  quickFilters: readonly QuickFilter[]
  /** The one the register is showing exactly, if any. */
  activeId: string | null
  /** Assistant suggestions waiting on a decision; zero hides the chip. */
  waiting: number
  /** Whether padding income rows are listed; absent offers no chip. */
  padding?: RegisterQuery['padding']
  onPadding?: (next: RegisterQuery['padding']) => void
  onChoose: (quick: QuickFilter) => void
  /** A new custom quick filter, starting from what the register shows. */
  onCreate: () => void
  onManage: () => void
  onClearAll: () => void
}

/**
 * The register card's toolbar: everything that narrows the rows (the Filter
 * popover, the quick filters, the date window, the search) and the result.
 */
export function QuickFilters({
  filter,
  narrowing,
  dates,
  search,
  summary,
  carried,
  quickFilters,
  activeId,
  waiting,
  padding,
  onPadding,
  onChoose,
  onCreate,
  onManage,
  onClearAll,
}: QuickFiltersProps) {
  const hasSaved = quickFilters.some((quick) => quick.group === 'saved')

  return (
    <div className="toolbar quickfilters">
      {filter}
      {narrowing}
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button size="sm" variant={activeId === null ? 'secondary' : 'primary'}>
            <Zap size={13} aria-hidden="true" /> Quick filters
            <ChevronDown size={13} aria-hidden="true" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start">
          {quickFilters.map((quick, index) => {
            const heading =
              index === 0 || quickFilters[index - 1]!.group !== quick.group
                ? GROUP_LABELS[quick.group]
                : null
            return (
              <Fragment key={quick.id}>
                {heading === null ? null : (
                  <>
                    <DropdownMenuSeparator />
                    <DropdownMenuLabel>{heading}</DropdownMenuLabel>
                  </>
                )}
                <DropdownMenuCheckboxItem
                  checked={quick.id === activeId}
                  onCheckedChange={() => onChoose(quick)}
                >
                  {quick.name}
                </DropdownMenuCheckboxItem>
              </Fragment>
            )
          })}
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={onCreate}>
            <BookmarkPlus size={13} aria-hidden="true" /> New custom filter…
          </DropdownMenuItem>
          {hasSaved ? (
            <DropdownMenuItem onSelect={onManage}>
              <Settings2 size={13} aria-hidden="true" /> Edit saved filters…
            </DropdownMenuItem>
          ) : null}
          <DropdownMenuItem onSelect={onClearAll}>Clear all</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      {dates}
      {summary ? (
        <span className="chip chip--result">
          {formatCount(summary.count)} results ·{' '}
          {summary.partial_count > 0 ? (
            // The matching splits, then the same rows whole: the first figure
            // agrees with a report over this filter, the second with the rows.
            <span
              className="chip__partial"
              title={`${plural(summary.partial_count, 'split transaction')} counted by the matching splits only`}
            >
              <Split size={12} aria-hidden="true" />
              <span className="visually-hidden">Matching splits: </span>
              <Money value={summary.total} tone="flow" showPlus />
              <span className="chip__secondary">
                of <Money value={summary.full_total} tone="neutral" showPlus /> in full
              </span>
            </span>
          ) : (
            <Money value={summary.total} tone="flow" showPlus />
          )}
        </span>
      ) : null}
      {/* Hidden padding is named with its sum, so the result beside it still
          reconciles with the opening and ending balances. */}
      {onPadding && padding === 'hide' && summary && summary.padding_count > 0 ? (
        <button
          type="button"
          className="chip"
          aria-pressed={false}
          title="Income recorded beside purchases paid by payroll deduction. It still counts in every balance and figure."
          onClick={() => onPadding('show')}
        >
          <HandCoins size={12} aria-hidden="true" />
          {plural(summary.padding_count, 'padding row')} hidden ·{' '}
          <Money value={summary.padding_total} tone="neutral" showPlus />
        </button>
      ) : null}
      {onPadding && padding === 'show' ? (
        <button
          type="button"
          className="chip chip--on"
          aria-pressed={true}
          onClick={() => onPadding('hide')}
        >
          <HandCoins size={12} aria-hidden="true" /> Padding shown
        </button>
      ) : null}
      {/* What the assistant is waiting on, shown where it is decided. */}
      {waiting > 0 ? (
        <span className="chip chip--result">
          <Sparkles size={12} aria-hidden="true" /> {plural(waiting, 'suggestion')} waiting
        </span>
      ) : null}
      {carried ? (
        <span className="quickfilters__label hint hint--faint">
          Opening <Money value={carried.opening_balance} tone="neutral" /> · Ending{' '}
          <Money value={carried.ending_balance} tone="neutral" />
        </span>
      ) : null}
      {/* Which date rows are filed under, and what a preset resolved to; the
          control already names the window. */}
      {summary ? (
        <span className="quickfilters__label hint hint--faint">
          {summary.window.from === null
            ? `On the ${summary.window.date_field} date`
            : `${formatDate(summary.window.from)} – ${
                summary.window.to === null ? 'today' : formatDate(summary.window.to)
              } on the ${summary.window.date_field} date`}
        </span>
      ) : null}
      {search ? (
        <>
          <span className="toolbar__spacer" />
          {search}
        </>
      ) : null}
    </div>
  )
}
