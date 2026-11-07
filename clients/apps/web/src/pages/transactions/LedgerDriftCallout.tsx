import { Link, useSearchParams } from 'react-router-dom'

import { useMoneyText } from '@/components/moneyText'
import { ledgerDriftText } from '@/components/shell/accountTree'
import { Button, Callout } from '@/components/ui'
import type { AccountWithBalances } from '@/lib/transactions/types'

const SUSPECT_TEXT = {
  duplicate: 'The difference is the amount of one row, which may be a duplicate.',
  missing: 'The difference is the amount of one row the other way, so a row like it may be missing.',
} as const

/**
 * An account whose rows have stopped adding up to the bank's balance. The
 * balance shown is still the bank's; the balances before the difference are
 * off by it (calculations.md §3, "Ledger drift").
 */
export function LedgerDriftCallout({ account }: { account: AccountWithBalances }) {
  const moneyText = useMoneyText()
  const [params] = useSearchParams()
  const drift = account.ledger_drift
  if (!drift) return null

  const suspect = drift.suspect
  let open = null
  if (suspect) {
    const next = new URLSearchParams(params)
    next.set('edit', suspect.transaction_id)
    open = (
      <Button size="sm" asChild>
        <Link to={{ search: `?${next.toString()}` }}>Open the row</Link>
      </Button>
    )
  }

  return (
    <Callout
      tone="warning"
      title="This account's transactions don't add up to the bank's balance"
      actions={open}
    >
      {ledgerDriftText(drift, moneyText)}{' '}
      {suspect
        ? SUSPECT_TEXT[suspect.kind]
        : 'A row around then is probably missing or counted twice.'}
    </Callout>
  )
}
