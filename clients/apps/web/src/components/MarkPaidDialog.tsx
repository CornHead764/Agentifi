import { useState } from 'react'

import {
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  Field,
  Input,
  MoneyInput,
} from '@/components/ui'
import type { AcceptEdits, Occurrence } from '@/lib/clients/upcoming'
import { formatDate } from '@/lib/format'
import { amountToWire, parseAmountInput } from '@/lib/money'

/**
 * Mark paid with the figures the biller actually charged: the same accept as
 * the reminder's tick, with amount, date, payee and note filled in from the
 * occurrence.
 *
 * The amount keeps its sign. The server writes the figure straight into the
 * register, so dropping the minus would file a bill as income.
 */
export function MarkPaidDialog({
  occurrence,
  pending,
  onCancel,
  onConfirm,
}: {
  occurrence: Occurrence
  pending: boolean
  onCancel: () => void
  onConfirm: (edits: AcceptEdits) => void
}) {
  const [amount, setAmount] = useState(() => amountToWire(occurrence.amount))
  const [date, setDate] = useState(occurrence.due_on)
  const [payee, setPayee] = useState(occurrence.label)
  const [notes, setNotes] = useState('')

  const parsed = parseAmountInput(amount)

  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onCancel())}>
      <DialogContent
        title={`Mark ${occurrence.label} paid`}
        description={`Due ${formatDate(occurrence.due_on)}.`}
        onSubmit={() => {
          if (!parsed) return
          onConfirm({
            amount: parsed.wire,
            date,
            payee: payee.trim() || occurrence.label,
            notes: notes.trim(),
          })
        }}
        footer={
          <DialogActions>
            <Button type="submit" variant="primary" disabled={!parsed || pending}>
              Mark paid
            </Button>
          </DialogActions>
        }
      >
        <Field label="Amount" hint="What the charge actually came to, not what was projected.">
          <MoneyInput
            value={amount}
            onChange={(event) => setAmount(event.target.value)}
          />
        </Field>
        <Field label="Date paid">
          <Input type="date" value={date} onChange={(event) => setDate(event.target.value)} />
        </Field>
        <Field label="Payee">
          <Input value={payee} onChange={(event) => setPayee(event.target.value)} />
        </Field>
        <Field label="Note (optional)">
          <Input value={notes} onChange={(event) => setNotes(event.target.value)} />
        </Field>
      </DialogContent>
    </Dialog>
  )
}
