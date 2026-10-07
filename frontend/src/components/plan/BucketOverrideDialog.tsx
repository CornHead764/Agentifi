import { useState } from 'react'

import { Money } from '@/components/Money'
import {
  Button,
  DialogActions,
  DialogContent,
  Field,
  FormDialog,
  MoneyInput,
  useHeld,
} from '@/components/ui'
import { BUCKET_LABELS, effectiveAmount, type BucketKey, type PlanBucket } from '@/lib/spendingPlan'
import { amountToWire, parseAmountInput } from '@/lib/money'

export interface BucketOverrideDialogProps {
  /** The bucket being overridden; null keeps the dialog closed. */
  bucket: PlanBucket | null
  bucketKey: BucketKey
  onOpenChange: (open: boolean) => void
  /** A decimal string. Clearing is the menu's own item, not an empty save here. */
  onSave: (amount: string) => void
}

/**
 * *Override this month's amount*. The amount keeps its sign: a bills bucket
 * is negative, the server stores what it is given, and dropping the minus
 * would turn the expenses into income.
 */
export function BucketOverrideDialog({
  bucket: openBucket,
  bucketKey,
  onOpenChange,
  onSave,
}: BucketOverrideDialogProps) {
  const bucket = useHeld(openBucket)
  return (
    <FormDialog open={openBucket !== null} onOpenChange={onOpenChange}>
      {bucket === null ? null : (
        <BucketOverrideForm bucket={bucket} bucketKey={bucketKey} onSave={onSave} />
      )}
    </FormDialog>
  )
}

function BucketOverrideForm({
  bucket,
  bucketKey,
  onSave,
}: Omit<BucketOverrideDialogProps, 'bucket' | 'onOpenChange'> & { bucket: PlanBucket }) {
  const [amount, setAmount] = useState(() => amountToWire(effectiveAmount(bucket)))
  const parsed = parseAmountInput(amount)

  return (
    <DialogContent
      title="Override this month's amount"
      description={BUCKET_LABELS[bucketKey]}
      onSubmit={() => {
        if (!parsed) return
        onSave(parsed.wire)
      }}
      footer={
        <DialogActions>
          <Button type="submit" variant="primary" disabled={!parsed}>
            Save
          </Button>
        </DialogActions>
      }
    >
      <Field
        label="Amount"
        hint="This month only; replaces the calculation. Reset override restores it."
      >
        <MoneyInput
          value={amount}
          onChange={(event) => setAmount(event.target.value)}
        />
      </Field>
      <p className="dialog__prose">
        Calculated from the rows this month:{' '}
        <Money value={bucket.calculated_amount} signs="absolute" tone="neutral" />.
      </p>
    </DialogContent>
  )
}
