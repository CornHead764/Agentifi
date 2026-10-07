import { Check, ExternalLink, Link2, Link2Off, Pencil, Repeat, SkipForward, X } from 'lucide-react'
import { Link } from 'react-router-dom'

import { Money } from '@/components/Money'
import {
  Badge,
  Button,
  DialogContent,
  FormDialog,
  SkeletonRows,
  Table,
  Td,
  Th,
  useHeld,
  useToast,
} from '@/components/ui'
import {
  SERIES_KIND_LABELS,
  acceptSlot,
  skipSlot,
  useSeriesHistory,
  type Series,
  type SeriesHistoryRow,
  invalidateOccurrenceWrites,
} from '@/lib/clients/upcoming'
import { describeApiError } from '@/lib/errors'
import { formatDate, formatMonthKey } from '@/lib/format'
import { useInvalidatingMutation } from '@/lib/queryClient'
import { shortLabel } from '@/lib/recurrence'
import { STATUS_LABELS, STATUS_TONES, type PlanEntry } from '@/lib/spendingPlan'
import { useAccountName, useTransaction } from '@/lib/transactions/queries'
import { registerLinkFor } from '@/lib/transactions/links'
import type { Transaction } from '@/lib/transactions/types'
import { displayPayee } from '@/lib/transactions/edits'

/**
 * What is behind one row of the plan. A recurring row shows its series and the
 * months before it (expected, what paid it, what was skipped); a posted row
 * shows the transaction. Both offer the ways to change the fit.
 */
export interface PlanEntryDialogProps {
  entry: PlanEntry | null
  /** `YYYY-MM`: the month the row was opened from, and the last one the history shows. */
  month: string
  /**
   * Money coming in rather than going out, which changes only the wording.
   * Taken from the bucket rather than the history, which arrives later, so a
   * button's verb does not change under the cursor.
   */
  isIncome: boolean
  frozen: boolean
  onOpenChange: (open: boolean) => void
  onExclude: (entry: PlanEntry) => void
  onEditSeries: (series: Series) => void
  onLinkSeries: (txn: Transaction) => void
  onUnlinkSeries: (txn: Transaction) => void
  onCreateSeries: (txn: Transaction) => void
}

export function PlanEntryDialog({
  entry: openEntry,
  month,
  isIncome,
  frozen,
  onOpenChange,
  onExclude,
  onEditSeries,
  onLinkSeries,
  onUnlinkSeries,
  onCreateSeries,
}: PlanEntryDialogProps) {
  const entry = useHeld(openEntry)
  return (
    <FormDialog open={openEntry !== null} onOpenChange={onOpenChange}>
      <DialogContent
        wide
        className="plan-detail"
        title={entry?.name ?? 'Row'}
        description={
          entry ? (
            <>
              {formatDate(entry.due_on)} ·{' '}
              <Money value={entry.amount} showPlus={entry.amount > 0} />
              {entry.category_name ? ` · ${entry.category_name}` : ''}
            </>
          ) : undefined
        }
      >
        {entry ? (
          <EntryBody
            entry={entry}
            month={month}
            isIncome={isIncome}
            frozen={frozen}
            onExclude={onExclude}
            onEditSeries={onEditSeries}
            onLinkSeries={onLinkSeries}
            onUnlinkSeries={onUnlinkSeries}
            onCreateSeries={onCreateSeries}
            onClose={() => onOpenChange(false)}
          />
        ) : null}
      </DialogContent>
    </FormDialog>
  )
}

function EntryBody({
  entry,
  month,
  isIncome,
  frozen,
  onExclude,
  onEditSeries,
  onLinkSeries,
  onUnlinkSeries,
  onCreateSeries,
  onClose,
}: Omit<PlanEntryDialogProps, 'entry' | 'onOpenChange'> & {
  entry: PlanEntry
  onClose: () => void
}) {
  const toast = useToast()
  const accountName = useAccountName()
  const transaction = useTransaction(entry.txn_id)
  const history = useSeriesHistory(entry.series_id, month)

  // A slot marked paid or skipped changes this month's bucket and the series'
  // own pointer, so everything that read either is asked again.
  const slot = useInvalidatingMutation(
    (action: 'accept' | 'skip') =>
      action === 'accept'
        ? acceptSlot(entry.series_id ?? '', entry.due_on)
        : skipSlot(entry.series_id ?? '', entry.due_on),
    invalidateOccurrenceWrites,
    {
      failure: 'That change did not save',
      onSuccess: (_, action) => {
        toast.show({
          title:
            action === 'accept' ? `${entry.name} marked as paid` : `${entry.name} skipped this month`,
        })
        onClose()
      },
    },
  )

  const series = history.data?.series ?? null
  const unfulfilled = entry.series_id !== null && entry.txn_id === null
  const txn = transaction.data ?? null

  return (
    <div className="plan-detail__body">
      {entry.txn_id !== null ? (
        <section className="plan-detail__section">
          <h3>Transaction</h3>
          {transaction.isPending ? (
            <SkeletonRows rows={2} />
          ) : transaction.isError || txn === null ? (
            <p className="plan-entries__note hint hint--faint">
              {transaction.isError ? describeApiError(transaction.error, 'load') : 'Not found.'}
            </p>
          ) : (
            <dl className="plan-detail__facts">
              <div>
                <dt>Date</dt>
                <dd>{formatDate(txn.date)}</dd>
              </div>
              <div>
                <dt>Account</dt>
                <dd>{accountName(txn.account_id)}</dd>
              </div>
              <div>
                <dt>Payee</dt>
                <dd>{displayPayee(txn)}</dd>
              </div>
              {txn.payee && txn.statement_name && txn.payee !== txn.statement_name ? (
                <div>
                  <dt>Statement</dt>
                  <dd>{txn.statement_name}</dd>
                </div>
              ) : null}
              <div>
                <dt>Amount</dt>
                <dd>
                  <Money value={txn.amount} showPlus={txn.amount > 0} />
                </dd>
              </div>
              {txn.notes ? (
                <div>
                  <dt>Notes</dt>
                  <dd>{txn.notes}</dd>
                </div>
              ) : null}
            </dl>
          )}
        </section>
      ) : null}

      {entry.series_id !== null ? (
        <section className="plan-detail__section">
          <h3>Recurring</h3>
          {history.isPending ? (
            <SkeletonRows rows={4} />
          ) : history.isError || !history.data ? (
            <p className="plan-entries__note hint hint--faint">
              {history.isError ? describeApiError(history.error, 'load') : 'Not found.'}
            </p>
          ) : (
            <>
              <p className="plan-detail__series">
                <Repeat size={13} aria-hidden="true" />
                <span>
                  {SERIES_KIND_LABELS[history.data.series.kind]} ·{' '}
                  {shortLabel(history.data.series.recurrence)} · expected{' '}
                  <Money value={history.data.series.amount} signs="absolute" tone="neutral" />
                  {history.data.average_paid !== null ? (
                    <>
                      {' '}
                      · averaged{' '}
                      <Money
                        value={history.data.average_paid}
                        signs="absolute"
                        tone="neutral"
                      />{' '}
                      over {history.data.paid_count}{' '}
                      {isIncome
                        ? history.data.paid_count === 1
                          ? 'deposit'
                          : 'deposits'
                        : history.data.paid_count === 1
                          ? 'payment'
                          : 'payments'}
                    </>
                  ) : null}
                </span>
              </p>
              <HistoryTable rows={history.data.rows} isIncome={isIncome} />
            </>
          )}
        </section>
      ) : null}

      <div className="plan-detail__actions">
        {txn ? (
          <Button variant="secondary" size="sm" asChild>
            <Link to={registerLinkFor(txn)}>
              <ExternalLink size={13} aria-hidden="true" /> Open in register
            </Link>
          </Button>
        ) : null}
        {series ? (
          <Button variant="secondary" size="sm" onClick={() => onEditSeries(series)}>
            <Pencil size={13} aria-hidden="true" /> Edit series
          </Button>
        ) : null}
        {unfulfilled ? (
          <>
            <Button
              variant="secondary"
              size="sm"
              disabled={frozen || slot.isPending}
              onClick={() => slot.mutate('accept')}
            >
              <Check size={13} aria-hidden="true" /> {isIncome ? 'Mark as received' : 'Mark as paid'}
            </Button>
            <Button
              variant="secondary"
              size="sm"
              disabled={frozen || slot.isPending}
              onClick={() => slot.mutate('skip')}
            >
              <SkipForward size={13} aria-hidden="true" /> Skip this month
            </Button>
          </>
        ) : null}
        {txn ? (
          <>
            <Button
              variant="secondary"
              size="sm"
              disabled={frozen}
              onClick={() => onLinkSeries(txn)}
            >
              <Link2 size={13} aria-hidden="true" />{' '}
              {entry.series_id ? 'Link to a different series' : 'Link to a series'}
            </Button>
            {entry.series_id ? (
              <Button
                variant="secondary"
                size="sm"
                disabled={frozen}
                onClick={() => onUnlinkSeries(txn)}
              >
                <Link2Off size={13} aria-hidden="true" /> Unlink from series
              </Button>
            ) : (
              <Button
                variant="secondary"
                size="sm"
                disabled={frozen}
                onClick={() => onCreateSeries(txn)}
              >
                <Repeat size={13} aria-hidden="true" /> Create a series from this
              </Button>
            )}
          </>
        ) : null}
        <Button variant="ghost" size="sm" disabled={frozen} onClick={() => onExclude(entry)}>
          <X size={13} aria-hidden="true" /> Exclude from this month
        </Button>
      </div>
    </div>
  )
}

/** Each month's slot beside what paid it, newest first; the actual is the charge's own amount. */
function HistoryTable({
  rows,
  isIncome,
}: {
  rows: readonly SeriesHistoryRow[]
  isIncome: boolean
}) {
  if (rows.length === 0) {
    return (
      <p className="plan-entries__note hint hint--faint">No occurrences in the last twelve months.</p>
    )
  }
  return (
    <Table density="sm" stack>
      <thead>
        <tr>
          <Th>Month</Th>
          <Th>Due</Th>
          <Th>Status</Th>
          <Th numeric>Expected</Th>
          <Th numeric>Actual</Th>
          <Th>{isIncome ? 'Received by' : 'Paid by'}</Th>
        </tr>
      </thead>
      <tbody>
        {rows.map((row) => (
          <tr key={`${row.due_on}:${row.transaction?.id ?? 'slot'}`}>
            <Td label="" className="stack-lead">
              {formatMonthKey(row.due_on.slice(0, 7), 'month')}
            </Td>
            <Td className="stack-inline">{formatDate(row.due_on, 'short')}</Td>
            <Td className="stack-inline">
              <Badge tone={STATUS_TONES[row.status]}>{STATUS_LABELS[row.status]}</Badge>
              {/* Still paid, but not on a slot the rule produces, so the month
                  beside it is the charge's own day. */}
              {row.off_schedule ? <Badge>Off schedule</Badge> : null}
            </Td>
            <Td numeric className="stack-inline">
              <Money value={row.expected} signs="absolute" tone="neutral" />
            </Td>
            <Td numeric className="stack-inline">
              {row.transaction ? (
                <Money value={row.transaction.amount} signs="absolute" tone="neutral" />
              ) : (
                '—'
              )}
            </Td>
            <Td className="stack-inline">
              {row.transaction ? (
                <Link to={registerLinkFor(row.transaction)} className="plan-detail__paidby">
                  {formatDate(row.transaction.date, 'short')} · {row.transaction.account_name}
                </Link>
              ) : (
                <span className="muted">Nothing yet</span>
              )}
            </Td>
          </tr>
        ))}
      </tbody>
    </Table>
  )
}
