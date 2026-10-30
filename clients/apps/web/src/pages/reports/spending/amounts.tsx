import { ArrowDown, ArrowUp } from 'lucide-react'

import { Money } from '@/components/Money'
import { Badge } from '@/components/ui'
import type { SpendingDifference } from '@/lib/clients/reports'
import { formatPercentUnits, parseRate } from '@/lib/format'
import { absMoney, type Money as MoneyValue } from '@/lib/money'

export type DifferenceUnit = 'pct' | 'amount'

/** A line's spend printed as spend: a charge as a plain figure, a net credit as a green "+". */
export function SpendAmount({ value, showCents }: { value: MoneyValue; showCents: boolean }) {
  if (value > 0) return <Money value={value} showPlus tone="signed" showCents={showCents} />
  return <Money value={value} signs="spend" tone="neutral" showCents={showCents} />
}

/**
 * The difference column's chip: up and coral for more spent, down and green
 * for less, or the words for a line with nothing on one side.
 */
export function DifferenceChip({
  difference,
  unit,
  partial,
  showCents,
}: {
  difference: SpendingDifference
  unit: DifferenceUnit
  /** The period is still running, so nothing yet may become something. */
  partial: boolean
  showCents: boolean
}) {
  switch (difference.state) {
    case 'none':
      return null
    case 'new_spend':
      return <Badge>New spend</Badge>
    case 'no_spend':
      return <Badge>{partial ? 'No spend yet' : 'No spend'}</Badge>
  }
  const more = difference.amount > 0
  const less = difference.amount < 0
  const percent = parseRate(difference.pct)
  return (
    <Badge tone={more ? 'expense' : less ? 'income' : 'neutral'} className="spend-diff">
      {more ? <ArrowUp size={11} aria-hidden="true" /> : null}
      {less ? <ArrowDown size={11} aria-hidden="true" /> : null}
      <span className="visually-hidden">{more ? 'More spent by ' : less ? 'Less spent by ' : 'No change, '}</span>
      {unit === 'pct' ? (
        formatPercentUnits(percent === null ? null : Math.abs(percent), { digits: 1 })
      ) : (
        <Money value={absMoney(difference.amount)} tone="neutral" showCents={showCents} />
      )}
    </Badge>
  )
}
