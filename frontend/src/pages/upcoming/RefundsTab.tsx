import { useState } from 'react'

import { QueryBoundary } from '@/components/QueryBoundary'
import { SeriesEditor } from '@/components/SeriesEditor'
import { Card } from '@/components/ui'
import { useRefunds, type Series } from '@/lib/clients/upcoming'
import { useAccounts, useCategories } from '@/lib/transactions/queries'

import { RefundTable } from './RefundTable'

/**
 * Refunds: a refund is a series (an expected credit), so this is the ordinary
 * series machinery pointed at one kind, and marking one received is the same
 * accept that pays a bill.
 */
export function RefundsTab() {
  const refunds = useRefunds()
  const accounts = useAccounts()
  const categories = useCategories()
  const [editing, setEditing] = useState<Series | null>(null)

  return (
    <>
      <QueryBoundary query={refunds} rows={6}>
        {(data) => (
          <div className="stack">
            <Card title="Expected" flush>
              <RefundTable
                rows={data.expected}
                empty="No refunds tracked. Track money due back from a return."
                onEdit={setEditing}
              />
            </Card>

            <Card title="Completed" flush>
              <RefundTable rows={data.completed} empty="No completed refunds yet." onEdit={setEditing} />
            </Card>
          </div>
        )}
      </QueryBoundary>

      <SeriesEditor
        open={editing !== null}
        onOpenChange={(open) => {
          if (!open) setEditing(null)
        }}
        series={editing}
        fixedKind="refund"
        accounts={accounts.data ?? []}
        categories={categories.data ?? []}
        tags={[]}
      />
    </>
  )
}
