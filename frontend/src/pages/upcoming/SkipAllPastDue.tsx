import { SkipForward } from 'lucide-react'

import { runInOrder } from '@/components/plan/bulkEnvelope'
import { Button, ConfirmDialog, useConfirm, useToast } from '@/components/ui'
import {
  invalidateOccurrenceWrites,
  skipOccurrence,
  type Occurrence,
} from '@/lib/clients/upcoming'
import { plural } from '@/lib/format'
import { useInvalidatingMutation } from '@/lib/queryClient'

import { skippedToast } from './skippedToast'

/**
 * Skip every past-due reminder at once, deliberately not mark them paid: a
 * bulk mark-paid would write a payment per reminder at an amount nobody
 * checked. Skipping writes no money.
 *
 * One request at a time, since there is no bulk endpoint, and a partial
 * failure leaves the rest skipped. The toast says how many went through.
 */
export function SkipAllPastDue({ occurrences }: { occurrences: readonly Occurrence[] }) {
  const { show } = useToast()
  const skipAll = useConfirm(
    useInvalidatingMutation(
      () => runInOrder(occurrences, skipOccurrence),
      invalidateOccurrenceWrites,
    ),
    { onSuccess: (result) => show(skippedToast(result)) },
  )

  return (
    <>
      <Button size="sm" variant="secondary" onClick={() => skipAll.ask()}>
        <SkipForward size={13} aria-hidden="true" /> Skip all
      </Button>
      <ConfirmDialog
        {...skipAll.dialog}
        title={`Skip ${plural(occurrences.length, 'past-due reminder')}?`}
        description="Each is marked not paid and its series moves to the next date. No transaction is recorded, so mark a bill you did pay as paid instead."
        confirmLabel={skipAll.dialog.pending ? 'Skipping…' : 'Skip them all'}
      />
    </>
  )
}
