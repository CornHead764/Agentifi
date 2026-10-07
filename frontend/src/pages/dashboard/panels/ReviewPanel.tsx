import { ChevronRight, Sparkles } from 'lucide-react'
import { useMemo } from 'react'
import { Link } from 'react-router-dom'

import { MoneyOrDash } from '@/components/figures'
import { QueryBoundary } from '@/components/QueryBoundary'
import { Button, Callout } from '@/components/ui'
import { usePendingAutomationActions } from '@/lib/clients/automations'
import { useAllTimeTile, useReviewTile } from '@/lib/clients/dashboard'
import { plural } from '@/lib/format'
import type { Money as MoneyValue } from '@/lib/money'
import { EMPTY_DRAFT, toFilterItems } from '@/lib/transactions/filter'
import { missingReceiptsLink, reviewQueueLink } from '@/lib/transactions/links'
import { useAccounts, useAdHocFilter, useCategories } from '@/lib/transactions/queries'

/**
 * Review Transactions. Three tiles: "Large amount(s)" is left out because
 * nothing in the domain defines large, and an invented threshold would be a
 * figure no other surface reproduces. A fourth, the rows missing a receipt, is
 * there only while some account requires receipts, and counts over all time
 * and whether or not the row is marked: a receipt is owed until it is filed.
 *
 * The narrowed tiles need a stored `Filter` first: `/transactions` takes a
 * `filter_id` and cannot carry facets inline.
 */
export function ReviewPanel() {
  const categories = useCategories()
  const universe = useMemo(
    () => ({ categories: categories.data ?? [], tags: [] }),
    [categories.data],
  )

  const uncategorizedItems = useMemo(
    () => toFilterItems({ ...EMPTY_DRAFT, uncategorized: true }, universe),
    [universe],
  )
  const billItems = useMemo(
    () => toFilterItems({ ...EMPTY_DRAFT, isBillOrSubscription: true }, universe),
    [universe],
  )

  const accounts = useAccounts()
  const holdsReceipts = (accounts.data ?? []).some((one) => one.requires_receipts)
  const receiptItems = useMemo(
    () => (holdsReceipts ? toFilterItems({ ...EMPTY_DRAFT, missingReceipt: true }, universe) : []),
    [holdsReceipts, universe],
  )

  const uncategorizedFilter = useAdHocFilter(uncategorizedItems, '')
  const billFilter = useAdHocFilter(billItems, '')
  const receiptFilter = useAdHocFilter(receiptItems, '')

  const all = useReviewTile(null)
  const uncategorized = useReviewTile(
    uncategorizedFilter.filterId,
    30,
    uncategorizedFilter.filterId !== null,
  )
  const bills = useReviewTile(billFilter.filterId, 30, billFilter.filterId !== null)
  const receipts = useAllTimeTile(receiptFilter.filterId, receiptFilter.filterId !== null)
  // What the assistant is waiting on. A suggestion is decided on its own
  // transaction, so this is a reason to open the register.
  const suggestions = usePendingAutomationActions()
  const proposed = suggestions.data?.length ?? 0

  return (
    <QueryBoundary query={all} rows={4}>
      {(data) => (
        <>
          {/* Above the tiles: the one line with decisions already waiting. */}
          {proposed > 0 ? (
            <Callout
              className="review-proposed"
              icon={<Sparkles size={14} />}
              actions={
                <Button asChild size="sm">
                  <Link to={reviewQueueLink()}>Review</Link>
                </Button>
              }
            >
              <p>
                <strong>{plural(proposed, 'change')}</strong> proposed by the assistant
              </p>
            </Callout>
          ) : null}

          <div className="tiles">
            <Tile
              label="Last 30 days"
              count={data.count}
              total={data.total}
              href={reviewQueueLink()}
            />
            <Tile
              label="Uncategorized"
              count={uncategorized.data?.count}
              total={uncategorized.data?.total}
              href={reviewQueueLink({ uncategorized: true })}
            />
            <Tile
              label="Bills & Subscriptions"
              count={bills.data?.count}
              total={bills.data?.total}
              href={reviewQueueLink({ isBill: true })}
            />
            {holdsReceipts ? (
              <Tile
                label="Missing receipts"
                count={receipts.data?.count}
                total={receipts.data?.total}
                href={missingReceiptsLink()}
              />
            ) : null}
          </div>
        </>
      )}
    </QueryBoundary>
  )
}

function Tile({
  label,
  count,
  total,
  href,
}: {
  label: string
  count: number | undefined
  total: MoneyValue | undefined
  href: string
}) {
  return (
    <Link to={href} className="tile">
      <span className="tile__label">
        {label}
        {/* Each tile opens the register on its filter, with the same chevron as
            "Review all". */}
        <ChevronRight size={14} aria-hidden="true" className="tile__go" />
      </span>
      <span className="muted">
        {count === undefined ? (
          'Loading…'
        ) : (
          <>
            {plural(count, 'transaction')}, totalling{' '}
            <MoneyOrDash value={total ?? null} tone="neutral" />
          </>
        )}
      </span>
    </Link>
  )
}
