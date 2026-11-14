import { Link } from 'react-router-dom'

import { Money } from '@/components/Money'
import { Button, Sheet, SheetContent } from '@/components/ui'
import type { ReviewRow } from '@/lib/clients/review'
import { registerLinkFor } from '@/lib/transactions/links'
import { formatDate, formatPercent } from '@agentifi/core/format'

import { REASON_WORDS } from './words'

/**
 * Everything behind one row: the bank's wording, why it needs a look, the
 * suggestion's split and confidence, and every order or receipt line.
 */
export function RowSheet({
  row,
  onClose,
  onApprove,
}: {
  row: ReviewRow | null
  onClose: () => void
  onApprove: (row: ReviewRow) => void
}) {
  const suggestion = row?.suggestion ?? null
  const splits = suggestion && suggestion.splits.length > 0 ? suggestion.splits : (row?.splits ?? [])

  return (
    <Sheet open={row !== null} onOpenChange={(open) => (open ? undefined : onClose())}>
      <SheetContent title={row?.payee ?? ''} className="review-sheet">
        {row ? (
          <div className="review-sheet__body">
            <p className="review-sheet__when">
              {formatDate(row.date)} · {row.account_name}
              {row.account_mask ? ` ··${row.account_mask}` : ''}
            </p>
            <Money value={row.amount} className="review-sheet__amount" />
            <p className="review-sheet__wording">Bank wording: {row.statement_name}</p>
            {row.reason !== null ? (
              <p className="review-sheet__reason">{REASON_WORDS[row.reason]}</p>
            ) : null}

            {suggestion ? (
              <section className="review-sheet__block">
                <h3 className="review-sheet__heading">
                  {suggestion.splits.length > 0 ? 'Proposed split' : `Suggested: ${suggestion.category_name}`}
                </h3>
                {suggestion.confidence !== null ? (
                  <p className="review-sheet__note">
                    Confidence {formatPercent(suggestion.confidence, { digits: 0 })}
                    {suggestion.basis ? ` · ${suggestion.basis}` : ''}
                  </p>
                ) : (
                  <p className="review-sheet__note">No confidence recorded, so it counts as a guess.</p>
                )}
              </section>
            ) : null}

            {splits.length > 0 ? (
              <ul className="review-sheet__lines">
                {splits.map((split, index) => (
                  <li key={index} className="review-sheet__line">
                    <span>
                      {split.category_name || 'Uncategorized'}
                      {split.memo ? <span className="review-sheet__memo"> · {split.memo}</span> : null}
                    </span>
                    <Money value={split.amount} tone="neutral" />
                  </li>
                ))}
              </ul>
            ) : null}

            {row.support.map((support) => (
              <section key={`${support.kind}:${support.title}`} className="review-sheet__block">
                <h3 className="review-sheet__heading">{support.title}</h3>
                <ul className="review-sheet__lines">
                  {support.lines.map((line, index) => (
                    <li key={index} className="review-sheet__line">
                      {line}
                    </li>
                  ))}
                </ul>
              </section>
            ))}

            {row.notes ? <p className="review-sheet__note">{row.notes}</p> : null}

            <div className="review-sheet__actions">
              {row.reason !== 'possible_duplicate' ? (
                <Button onClick={() => onApprove(row)}>
                  {suggestion && suggestion.splits.length > 0 ? 'Approve split' : 'Approve'}
                </Button>
              ) : null}
              <Button variant="secondary" asChild>
                <Link
                  to={registerLinkFor(
                    { id: row.transaction_id, account_id: row.account_id, date: row.date },
                    { open: true },
                  )}
                >
                  Split or edit
                </Link>
              </Button>
            </div>
          </div>
        ) : null}
      </SheetContent>
    </Sheet>
  )
}
