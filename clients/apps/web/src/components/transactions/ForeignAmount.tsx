import { Money } from '@/components/Money'
import type { Transaction } from '@/lib/transactions/types'

/**
 * An amount that arrived in another currency. The converted amount, which
 * every total uses, is printed large; the bank's own figure beside it. The
 * rate belongs to the transaction detail.
 */
export function ForeignAmount({
  txn,
  showPlus = false,
}: {
  txn: Transaction
  showPlus?: boolean
}) {
  // `amount_primary` is only written for a row not in the space's currency.
  if (txn.amount_primary === null)
    return <Money value={txn.amount} tone="flow" showPlus={showPlus} />

  return (
    <span className="foreign-amount">
      <Money value={txn.amount_primary} tone="flow" showPlus={showPlus} />
      <span className="foreign-amount__original">
        <Money
          value={txn.amount}
          currency={txn.currency}
          tone="neutral"
          showPlus={showPlus}
        />
      </span>
    </span>
  )
}
