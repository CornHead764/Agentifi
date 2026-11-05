import { Check, Link2, SkipForward, Undo2 } from 'lucide-react'
import { useState, type ReactNode } from 'react'

import { IsThisItDialog } from '@/components/SuggestedPayment'
import { LinkPaymentDialog } from '@/components/transactions/LinkPaymentDialog'
import { ConfirmDialog, IconButton, Tooltip, useConfirm } from '@/components/ui'
import {
  markedSlot,
  type Occurrence,
  useAcceptOccurrence,
  useSkipOccurrence,
  useUnacceptOccurrence,
} from '@/lib/clients/upcoming'
import { formatDate } from '@agentifi/core/format'
import { paymentSlot } from '@/lib/transactions/paymentCandidates'
import { skipPrompt } from '@/lib/transactions/reminders'

/**
 * The three ways to settle one slot of a recurring item, told apart in words:
 * link a transaction already in the register, mark it paid, or skip this one
 * occurrence. `children` sit between the mark and skip, for the list's own
 * extras. A slot already marked paid offers the link and taking the mark back
 * instead; one paid by the transaction its mark wrote offers only taking it
 * back. A mark with rows that could be its payment asks "Is this it?".
 */
export function ReminderActions({
  occurrence,
  children,
}: {
  occurrence: Occurrence
  children?: ReactNode
}) {
  const accept = useAcceptOccurrence()
  const unaccept = useUnacceptOccurrence()
  const skip = useConfirm(useSkipOccurrence())
  const [linking, setLinking] = useState(false)
  const slot = occurrence.accepted_by_hand ? null : paymentSlot(occurrence)
  const marked = markedSlot(occurrence)
  const busy = accept.mark.isPending || unaccept.isPending || skip.dialog.pending
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
      {occurrence.marked_paid || occurrence.accepted_by_hand ? (
        <>
          {children}
          <Tooltip
            label={
              occurrence.accepted_by_hand
                ? 'Take back the mark and delete the transaction it recorded. Once that transaction has been changed, edit or delete it in the register instead.'
                : 'Take back the mark. It is expected again until a transaction is linked.'
            }
            side="top"
          >
            <IconButton
              label={`Unmark ${occurrence.label} as ${verb}`}
              size="sm"
              variant="ghost"
              disabled={busy}
              onClick={() => unaccept.mutate(occurrence)}
            >
              <Undo2 size={13} />
            </IconButton>
          </Tooltip>
        </>
      ) : (
        <>
          <Tooltip
            label={`Mark it ${verb}. It stops reminding you. Where a bank feeds the account it counts at the scheduled amount until the bank's transaction is linked; on an account no bank feeds, the transaction is recorded now.`}
            side="top"
          >
            <IconButton
              label={`Mark ${occurrence.label} as ${verb}`}
              size="sm"
              variant="ghost"
              disabled={busy}
              onClick={() => accept.mark.mutate(occurrence)}
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
        </>
      )}

      <LinkPaymentDialog slot={linking ? slot : null} onClose={() => setLinking(false)} />
      {marked === null ? null : (
        <IsThisItDialog slot={marked} candidates={accept.offered} onClose={accept.answered} />
      )}
      <ConfirmDialog
        {...skip.dialog}
        {...(skip.target ? skipPrompt(skip.target) : { title: 'Skip this reminder?' })}
        confirmLabel={skip.dialog.pending ? 'Skipping…' : 'Skip it'}
      />
    </>
  )
}
