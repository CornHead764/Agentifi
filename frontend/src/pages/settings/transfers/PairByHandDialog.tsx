import { useState } from 'react'
import { Link2 } from 'lucide-react'

import { useMoneyText } from '@/components/moneyText'
import {
  Button,
  DialogActions,
  DialogContent,
  DialogTrigger,
  EmptyState,
  Field,
  FormDialog,
  OptionSelect,
  SkeletonRows,
  type SelectOption,
} from '@/components/ui'
import { formatDate } from '@/lib/format'
import type { MoneyFormatter } from '@/lib/money'
import { useTransferCandidates, type TransferLeg } from '@/lib/clients/transfers'
import type { Uuid } from '@/lib/transactions/types'

/**
 * Joining two existing rows as one transfer, for what the automatic pairer
 * cannot see (a wire short by its fee, a late landing, a hand-entered leg).
 * The server waives the source rule for this path only. The sides are picked
 * separately because the pair is directional: two withdrawals must not pair.
 */
export function PairByHandDialog({
  onPair,
  pending,
}: {
  onPair: (payingId: Uuid, receivingId: Uuid) => Promise<unknown>
  pending: boolean
}) {
  const [open, setOpen] = useState(false)
  return (
    <FormDialog
      open={open}
      onOpenChange={setOpen}
      trigger={
        <DialogTrigger asChild>
          <Button variant="primary" size="sm">
            <Link2 size={13} /> Pair by hand
          </Button>
        </DialogTrigger>
      }
    >
      <PairForm
        open={open}
        pending={pending}
        onPair={async (payingId, receivingId) => {
          await onPair(payingId, receivingId)
          setOpen(false)
        }}
      />
    </FormDialog>
  )
}

function PairForm({
  open,
  onPair,
  pending,
}: {
  open: boolean
  onPair: (payingId: Uuid, receivingId: Uuid) => Promise<void>
  pending: boolean
}) {
  const [payingId, setPayingId] = useState('')
  const [receivingId, setReceivingId] = useState('')

  const candidates = useTransferCandidates(open)
  const rows = candidates.data?.candidates ?? []
  const moneyText = useMoneyText()
  const paying = rows.filter((leg) => leg.amount < 0)
  const receiving = rows.filter((leg) => leg.amount > 0)

  const chosenPaying = rows.find((leg) => leg.transaction_id === payingId)
  const chosenReceiving = rows.find((leg) => leg.transaction_id === receivingId)
  const sameAccount =
    chosenPaying !== undefined &&
    chosenReceiving !== undefined &&
    chosenPaying.account_id === chosenReceiving.account_id

  const submit = async () => {
    if (!payingId || !receivingId || sameAccount) return
    await onPair(payingId, receivingId)
  }

  return (
    <DialogContent
      title="Pair a transfer by hand"
      description="Join the two halves of a move between your accounts. Both rows stay; they stop counting as income and expense."
      wide
      footer={
        <DialogActions>
          <Button
            variant="primary"
            disabled={!payingId || !receivingId || sameAccount || pending}
            onClick={() => void submit()}
          >
            {pending ? 'Pairing…' : 'Pair these two'}
          </Button>
        </DialogActions>
      }
    >
      {candidates.isPending ? <SkeletonRows rows={3} /> : null}
      {candidates.isSuccess && rows.length === 0 ? (
        <EmptyState
          compact
          title="Nothing left to pair; every transaction is already in a transfer."
        />
      ) : null}
      {rows.length > 0 ? (
        <>
          <Field
            label="Money out"
            hint="The account the money left. Only unpaired transactions are offered."
          >
            <OptionSelect
              value={payingId}
              onValueChange={setPayingId}
              placeholder="Select the withdrawal"
              options={paying.map((leg) => legOption(leg, moneyText))}
            />
          </Field>
          <Field
            label="Money in"
            hint="Where it arrived. Amounts need not match, e.g. a fee taken in transit."
            error={sameAccount ? 'Both halves are in the same account.' : undefined}
          >
            <OptionSelect
              value={receivingId}
              onValueChange={setReceivingId}
              placeholder="Select the deposit"
              options={receiving.map((leg) => legOption(leg, moneyText))}
            />
          </Field>
        </>
      ) : null}
    </DialogContent>
  )
}

/** A row as a choice, on one line that identifies it: where, when, who, and how much. */
function legOption(leg: TransferLeg, moneyText: MoneyFormatter): SelectOption {
  const amount = moneyText(leg.amount, { currency: leg.currency, signs: 'absolute' })
  const payee = leg.payee || 'No payee'
  return {
    value: leg.transaction_id,
    label: `${leg.account_name} · ${formatDate(leg.date, 'short')} · ${payee} · ${amount}`,
  }
}
