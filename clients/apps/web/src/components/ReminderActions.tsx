import { Check, Link2, SkipForward } from 'lucide-react'
import { useState, type ReactNode } from 'react'

import { LinkPaymentDialog } from '@/components/transactions/LinkPaymentDialog'
import { ConfirmDialog, IconButton, Tooltip, useConfirm } from '@/components/ui'
import {
  type Occurrence,
  useAcceptOccurrence,
  useSkipOccurrence,
} from '@/lib/clients/upcoming'
import { formatDate } from '@agentifi/core/format'
import { paymentSlot } from '@/lib/transactions/paymentCandidates'
import { skipPrompt } from '@/lib/transactions/reminders'

/**
 * The three ways to settle one slot of a recurring item, told apart in words:
 * link a transaction already in the register, record a payment now, or skip
 * this one occurrence. `children` sit between record and skip, for the list's
 * own extras.
 */
export function ReminderActions({
  occurrence,
  children,
}: {
  occurrence: Occurrence
  children?: ReactNode
}) {
  const accept = useAcceptOccurrence()
  const skip = useConfirm(useSkipOccurrence())
  const [linking, setLinking] = useState(false)
  const slot = paymentSlot(occurrence)
  const busy = accept.isPending || skip.dialog.pending
  const verb = occurrence.kind === 'income' ? 'received' : 'paid'

  return (
    <>
      {slot === null ? null : (
        <Tooltip label={`Find a transaction already in the register and mark this ${verb} by it`} side="top">
          <IconButton
            label={`Link a transaction to ${occurrence.label}`}
            size="sm"
            variant="ghost"
            onClick={() => setLinking(true)}
          >
            <Link2 size={13} />
          </IconButton>
        </Tooltip>
      )}
      <Tooltip
        label={`Record it ${verb} now at the scheduled amount. This adds a transaction; if the bank already has it, link that one instead.`}
        side="top"
      >
        <IconButton
          label={`Record ${occurrence.label} as ${verb}`}
          size="sm"
          variant="ghost"
          disabled={busy}
          onClick={() => accept.mutate({ occurrence })}
        >
          <Check size={13} />
        </IconButton>
      </Tooltip>
      {children}
      <Tooltip
        label="Skip only this one, as if it is not happening. Nothing is recorded, the spending plan stops expecting it, and later ones carry on."
        side="top"
      >
        <IconButton
          label={`Skip ${occurrence.label} on ${formatDate(occurrence.due_on)}`}
          size="sm"
          variant="ghost"
          disabled={busy}
          onClick={() => skip.ask(occurrence)}
        >
          <SkipForward size={13} />
        </IconButton>
      </Tooltip>

      <LinkPaymentDialog slot={linking ? slot : null} onClose={() => setLinking(false)} />
      <ConfirmDialog
        {...skip.dialog}
        {...(skip.target ? skipPrompt(skip.target) : { title: 'Skip this reminder?' })}
        confirmLabel={skip.dialog.pending ? 'Skipping…' : 'Skip it'}
      />
    </>
  )
}
