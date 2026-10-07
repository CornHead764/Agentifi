import { Button, Td } from '@/components/ui'

import { smallBalancesLabel } from './accountTree'

/**
 * The table row that stands in for the rows `withoutSmallBalances` left out,
 * so an account that holds too little to list is one click away rather than
 * silently gone. Draws nothing when nothing was hidden.
 */
export function SmallBalancesRow({
  colSpan,
  hidden,
  revealed,
  onToggle,
}: {
  colSpan: number
  hidden: number
  revealed: boolean
  onToggle: () => void
}) {
  if (hidden === 0) return null
  return (
    <tr>
      <Td colSpan={colSpan}>
        <Button variant="ghost" size="sm" aria-expanded={revealed} onClick={onToggle}>
          {smallBalancesLabel(hidden, revealed)}
        </Button>
      </Td>
    </tr>
  )
}
