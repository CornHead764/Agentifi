import { Check } from 'lucide-react'

import { IconButton } from '@/components/ui'
import { useMarkStatementPaid } from '@/lib/clients/bills'
import type { Occurrence } from '@/lib/clients/upcoming'

/**
 * Ends a pay-manually reminder: its statement was paid where the ledger cannot
 * see. A payment the bank feed carries ends it without this.
 */
export function MarkStatementPaidButton({ occurrence }: { occurrence: Occurrence }) {
  const mark = useMarkStatementPaid()
  const billId = occurrence.bill?.id
  if (!billId) return null
  return (
    <IconButton
      label={`Mark ${occurrence.label} paid`}
      size="sm"
      variant="ghost"
      disabled={mark.isPending}
      onClick={() => mark.mutate(billId)}
    >
      <Check size={13} />
    </IconButton>
  )
}
