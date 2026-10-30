import { Money } from '@/components/Money'
import { Button, SkeletonRows, Table, TableEmptyRow, Td, Th } from '@/components/ui'
import { categoryName as categoryNameFor } from '@/lib/categoryNames'
import { formatDate } from '@/lib/format'
import { reportingDate } from '@/lib/transactions/aggregate'
import { displayPayee } from '@/lib/transactions/edits'
import { useCategories } from '@/lib/transactions/queries'

import type { useMatchingRows } from './matching-rows'

/** The rows `useMatchingRows` found, read-only, a page at a time. */
export function MatchingRowsTable({
  matching,
  empty,
  showCents = true,
}: {
  matching: ReturnType<typeof useMatchingRows>
  empty: string
  showCents?: boolean
}) {
  const categories = useCategories()
  const { rows, items } = matching
  const categoryName = (id: string | null) => categoryNameFor(categories.data ?? [], id)

  if (rows.isPending) return <SkeletonRows rows={5} />
  return (
    <>
      <Table density="sm" stack>
        <thead>
          <tr>
            <Th>Date</Th>
            <Th>Payee</Th>
            <Th>Category</Th>
            <Th numeric>Amount</Th>
          </tr>
        </thead>
        <tbody>
          {items.length === 0 ? (
            <TableEmptyRow colSpan={4}>{empty}</TableEmptyRow>
          ) : (
            items.map((txn) => (
              <tr key={txn.id}>
                <Td className="nowrap">{formatDate(reportingDate(txn))}</Td>
                <Td label="" className="stack-lead">
                  <span className="cell__clip cell__clip--wide" title={displayPayee(txn)}>
                    {displayPayee(txn)}
                  </span>
                </Td>
                <Td>
                  <span className="cell__clip" title={txn.splits.length > 0 ? 'Split' : categoryName(txn.category_id)}>
                    {txn.splits.length > 0 ? 'Split' : categoryName(txn.category_id)}
                  </span>
                </Td>
                <Td numeric>
                  <Money value={txn.amount} showCents={showCents} />
                </Td>
              </tr>
            ))
          )}
        </tbody>
      </Table>

      {rows.hasNextPage ? (
        <div className="matching-rows__more">
          <Button
            variant="ghost"
            disabled={rows.isFetchingNextPage}
            onClick={() => void rows.fetchNextPage()}
          >
            Load more
          </Button>
        </div>
      ) : null}
    </>
  )
}
