import { clsx } from 'clsx'
import { Check } from 'lucide-react'

import { Money } from '@/components/Money'
import { CategoryPicker } from '@/components/transactions/Pickers'
import { Button } from '@/components/ui'
import { looksRight, type ReviewDay, type ReviewRow } from '@/lib/clients/review'
import type { Category, Uuid } from '@/lib/transactions/types'

import { chipText, dayLabel, REASON_WORDS } from './words'

export interface RowHandlers {
  selectedId: string | null
  approvedIds: ReadonlySet<string>
  /** The row whose category picker is open, from a click or the `c` key. */
  pickingId: string | null
  categories: readonly Category[]
  frequentCategoryIds: readonly Uuid[]
  onSelect: (row: ReviewRow) => void
  onOpen: (row: ReviewRow) => void
  onApprove: (row: ReviewRow) => void
  onPick: (row: ReviewRow | null) => void
  onFile: (row: ReviewRow, categoryId: string) => void
  onApproveDay: (day: ReviewDay) => void
}

/** One effective date's new rows, with the day's money out and its "Approve" for the sure ones. */
export function DaySection({ day, handlers }: { day: ReviewDay; handlers: RowHandlers }) {
  const open = day.rows.filter((row) => !handlers.approvedIds.has(row.transaction_id))
  const sure = open.filter((row) => looksRight(row))

  return (
    <section className="review-day" aria-label={dayLabel(day.date)}>
      <header className="review-day__head">
        <div className="review-day__titles">
          <h3 className="review-day__title">{dayLabel(day.date)}</h3>
          <p className="review-day__sub">
            <Money value={day.out} tone="neutral" /> out
            {day.needs_a_look > 0 ? (
              <>
                {' · '}
                <strong>{day.needs_a_look} need a look</strong>
              </>
            ) : null}
          </p>
        </div>
        {sure.length > 0 ? (
          <Button size="sm" variant="secondary" onClick={() => handlers.onApproveDay(day)}>
            {sure.length === open.length ? `Approve ${sure.length}` : `Approve ${sure.length} of ${open.length}`}
          </Button>
        ) : null}
      </header>
      <ul className="review-day__rows">
        {day.rows.map((row) => (
          <RowView key={row.transaction_id} row={row} handlers={handlers} />
        ))}
      </ul>
    </section>
  )
}

function RowView({ row, handlers }: { row: ReviewRow; handlers: RowHandlers }) {
  const approved = handlers.approvedIds.has(row.transaction_id)
  const selected = handlers.selectedId === row.transaction_id
  const chip = chipText(row)
  const duplicate = row.reason === 'possible_duplicate'
  const note =
    row.reason !== 'unspecified' && row.reason !== 'guess' && row.reason !== 'no_category'
      ? REASON_WORDS[row.reason]
      : null
  const support = row.support[0]

  return (
    <li
      className={clsx('review-row', approved && 'review-row--approved')}
      data-row-id={row.transaction_id}
      aria-current={selected ? 'true' : undefined}
      tabIndex={selected ? 0 : -1}
      onFocus={() => handlers.onSelect(row)}
    >
      <button type="button" className="review-row__open" onClick={() => handlers.onOpen(row)}>
        <span className="review-row__payee">{row.payee}</span>
        {row.is_pending ? <span className="review-row__pending">Pending</span> : null}
      </button>
      <div className="review-row__meta">
        <CategoryPicker
          value={row.suggestion?.category_id ?? row.category_id}
          categories={handlers.categories}
          frequentIds={handlers.frequentCategoryIds}
          open={handlers.pickingId === row.transaction_id}
          onOpenChange={(open) => handlers.onPick(open ? row : null)}
          onChange={(id) => {
            if (id) handlers.onFile(row, id)
          }}
          trigger={
            <button
              type="button"
              className={clsx(
                'review-chip',
                chip.guess && 'review-chip--guess',
                chip.missing && 'review-chip--missing',
              )}
            >
              {chip.text}
              {chip.guess ? <span className="review-chip__note"> · a guess</span> : null}
            </button>
          }
        />
        <span className="review-row__account">
          {row.account_name}
          {row.account_mask ? ` ··${row.account_mask}` : ''}
        </span>
      </div>
      <Money value={row.amount} className="review-row__amount" />
      {duplicate ? (
        <span className="review-row__tick review-row__tick--blocked" title="Decide the duplicate above first" />
      ) : (
        <button
          type="button"
          className="review-row__tick"
          aria-pressed={approved}
          aria-label={approved ? `Approved ${row.payee}` : `Approve ${row.payee}`}
          onClick={() => handlers.onApprove(row)}
        >
          <Check size={16} aria-hidden="true" />
        </button>
      )}
      {support || note ? (
        <p className="review-row__support">
          {note ? <span className="review-row__reason">{note}</span> : null}
          {support ? (
            <span>
              <strong>{support.title}:</strong> {support.lines.slice(0, 3).join(', ')}
              {support.lines.length > 3 ? '…' : ''}
            </span>
          ) : null}
        </p>
      ) : null}
    </li>
  )
}
