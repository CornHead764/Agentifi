import { useMemo } from 'react'

import { MoneyOrDash } from '@/components/figures'
import { Money } from '@/components/Money'
import { QueryBoundary } from '@/components/QueryBoundary'
import { Badge, List, ListRow } from '@/components/ui'
import { useRecentSpend, useRecentTransactions } from '@/lib/clients/dashboard'
import { formatDate } from '@/lib/format'
import { useAccounts } from '@/lib/transactions/queries'
import type { Uuid } from '@/lib/transactions/types'

import { recentAccountIds } from '@/lib/accountScope'

/**
 * The five most recent transactions, and what has left in the last week. Both
 * read the same accounts: the Customize pick, or the everyday ones
 * (`lib/accountScope`). A figure over the whole ledger above a scoped list is
 * trap 5.
 *
 * Neither query fires until `/accounts` has answered, since the scope is
 * derived from it.
 */
export function RecentTransactionsPanel({ accounts: chosen }: { accounts?: Uuid[] }) {
  const accounts = useAccounts()
  const ready = accounts.data !== undefined
  const scope = useMemo(
    () => recentAccountIds(chosen, accounts.data ?? []),
    [chosen, accounts.data],
  )

  const rows = useRecentTransactions(scope, 5, ready)
  // The caption and the query read the same frozen bounds, so crossing
  // midnight cannot make the sentence name another window.
  const spend = useRecentSpend(scope, 7, ready)

  return (
    <QueryBoundary query={rows} rows={5}>
      {(page) => (
        <>
          <p className="widget__lead">
            <MoneyOrDash
              value={spend.data?.total ?? null}
              signs="absolute"
              tone="neutral"
              reason="Spend for this window has not loaded."
            />{' '}
            spent from {formatDate(spend.bounds.from, 'short')} – Today
          </p>
          <List>
            {page.items.map((row) => (
              <ListRow
                key={row.id}
                title={row.payee}
                // Authorised, not settled: the amount can still change and it
                // is not yet in the balance.
                badge={row.is_pending ? <Badge>Pending</Badge> : null}
                sub={row.statement_name}
                figures={<Money value={row.amount} tone="flow" showPlus />}
                figuresSub={formatDate(row.date, 'short')}
              />
            ))}
          </List>
        </>
      )}
    </QueryBoundary>
  )
}
